package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/api"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/bitrix"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/queue"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/telemetry"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/watchdog"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
)

func main() {
	// ─── Logger ──────────────────────────────────────────────────────────
	log, _ := zap.NewProduction()
	defer log.Sync()

	// ─── Config ──────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}
	log.Info("config loaded", zap.String("env", cfg.App.Env))

	// ─── Contexto com cancelamento ───────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ─── PostgreSQL ──────────────────────────────────────────────────────
	pool, err := db.NewPool(ctx, &cfg.Postgres, log)
	if err != nil {
		log.Fatal("postgres connect failed", zap.Error(err))
	}
	defer pool.Close()
	repo := db.NewRepository(pool)

	// ─── Redis ───────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("redis connect failed", zap.Error(err))
	}
	log.Info("Redis connected", zap.String("addr", cfg.Redis.Addr()))
	defer rdb.Close()

	// ─── Métricas ────────────────────────────────────────────────────────
	metrics := telemetry.New()

	// ─── Fila ────────────────────────────────────────────────────────────
	q := queue.New(rdb, &cfg.Queue, log)
	workers := queue.NewWorkerPool(q, cfg.Queue.Workers, log)

	// ─── Bitrix24 (multi-tenant) ──────────────────────────────────────────
	// Client é stateless em relação a tenants — recebe TenantCreds por chamada.
	// A conta de cada sessão WA é buscada em bitrix_accounts pelo sessionJID.
	bitrixClient := bitrix.NewClient(repo, log)
	bitrixProcessor := bitrix.NewProcessor(bitrixClient, repo, log)

	// ─── WhatsApp Manager ────────────────────────────────────────────────
	// Cria manager sem handler primeiro; handler é injetado após (precisa de waManager)
	waManager := whatsapp.NewManager(&cfg.WhatsApp, repo, log, nil)
	waManager.SetMessageHandler(buildMessageHandler(ctx, q, repo, waManager, metrics, log))

	// Carrega todas as sessões salvas no banco
	if err := waManager.LoadAll(ctx); err != nil {
		log.Warn("load sessions warning", zap.Error(err))
	}

	// ─── Workers inbound: WA → Bitrix ─────────────────────────────────────
	workers.StartInbound(ctx, func(c context.Context, job *queue.InboundJob) error {
		if err := bitrixProcessor.ProcessInbound(c, job); err != nil {
			metrics.BitrixErrors.Inc()
			return err
		}
		metrics.MessagesInbound.Inc()
		return nil
	})

	// ─── Workers outbound: Bitrix → WA ───────────────────────────────────
	workers.StartOutbound(ctx, func(c context.Context, job *queue.OutboundJob) error {
		metrics.MessagesOutbound.Inc()

		var waID string
		var err error

		// Mostra "digitando..." no WhatsApp antes de enviar — simula comportamento humano
		// e evita que o WA interprete envios rápidos como spam.
		typingDur := waManager.TypingDelay(job.Text)
		waManager.SendTyping(c, job.SessionJID, job.ToJID, typingDur)

		if job.FileURL != "" {
			// Baixa o arquivo do Bitrix
			fileData, dlErr := downloadURL(job.FileURL)
			if dlErr != nil {
				metrics.MessagesFailed.Inc()
				return fmt.Errorf("download file from bitrix: %w", dlErr)
			}
			mime := job.FileMime
			if mime == "" {
				mime = "application/octet-stream"
			}
			name := job.FileName
			if name == "" {
				name = "file"
			}
			// Roteamento por mime:
			// audio/mpeg (mp3) → áudio nativo WA
			// demais (wav, ogg, webm, etc) → documento
			log.Info("outbound file", zap.String("name", name), zap.String("mime", mime))
			if mime == "audio/mpeg" {
				waID, err = waManager.SendAudio(c, job.SessionJID, job.ToJID, fileData, mime, false)
				if err != nil {
					log.Warn("SendAudio (mp3) failed, falling back to SendDocument", zap.Error(err))
					waID, err = waManager.SendDocument(c, job.SessionJID, job.ToJID, fileData, mime, name)
				}
			} else {
				waID, err = waManager.SendDocument(c, job.SessionJID, job.ToJID, fileData, mime, name)
			}
		} else {
			waID, err = waManager.Send(c, job.SessionJID, job.ToJID, job.Text)
		}

		if err != nil {
			metrics.MessagesFailed.Inc()
			log.Error("outbound send failed",
				zap.String("session_jid", job.SessionJID),
				zap.String("to_jid", job.ToJID),
				zap.String("text_preview", func() string {
					if len(job.Text) > 80 {
						return job.Text[:80]
					}
					return job.Text
				}()),
				zap.Error(err),
			)
			return err
		}

		// Salva mensagem outbound no banco para contabilizar nas estatísticas
		msgType := db.MsgTypeText
		if job.FileURL != "" {
			msgType = db.MsgTypeDocument
		}
		now := time.Now()
		outMsg := &db.Message{
			ID:          uuid.New(),
			WAMessageID: waID,
			Direction:   db.DirOutbound,
			MessageType: msgType,
			Content:     job.Text,
			MediaMime:   job.FileMime,
			Status:      db.MsgDelivered,
			SentAt:      &now,
		}
		if err := repo.InsertMessage(c, outMsg); err != nil {
			log.Warn("insert outbound message failed", zap.String("wa_id", waID), zap.Error(err))
		}

		// Confirma delivery ao Bitrix para parar o spinner na mensagem do operador
		if job.BitrixConnector != "" && job.BitrixImMsgID != "" {
			log.Info("outbound delivery: confirming",
				zap.String("connector", job.BitrixConnector),
				zap.String("im_msg_id", job.BitrixImMsgID),
				zap.String("wa_id", waID))
			go func() {
				bgCtx := context.Background()
				acct, err := repo.GetBitrixAccountByJID(bgCtx, job.SessionJID)
				if err != nil {
					log.Warn("outbound delivery: bitrix account not found", zap.String("session", job.SessionJID), zap.Error(err))
					return
				}
				creds := bitrix.TenantCreds{
					Domain:       acct.Domain,
					ClientID:     acct.ClientID,
					ClientSecret: acct.ClientSecret,
					RedirectURI:  acct.RedirectURI,
				}
				if err := bitrixClient.ConnectorSetOutboundDelivery(
					bgCtx,
					creds,
					job.BitrixConnector,
					job.BitrixLine,
					job.BitrixImChatID,
					job.BitrixImMsgID,
					waID,
					job.BitrixChatExtID,
				); err != nil {
					log.Warn("outbound delivery confirmation failed", zap.Error(err))
				}
			}()
		} else {
			log.Warn("outbound delivery: skipped (missing connector or msg_id)",
				zap.String("connector", job.BitrixConnector),
				zap.String("im_msg_id", job.BitrixImMsgID))
		}
		return nil
	})

	// ─── Watchdog ────────────────────────────────────────────────────────
	wd := watchdog.New(waManager, repo, &cfg.Watchdog, log)
	wd.Start(ctx)

	// ─── HTTP Server ─────────────────────────────────────────────────────
	app := api.New(cfg, repo, waManager, bitrixClient, q, metrics, log)

	go func() {
		if err := app.Listen(":" + cfg.App.Port); err != nil {
			log.Error("http server error", zap.Error(err))
		}
	}()
	log.Info("HTTP server started", zap.String("port", cfg.App.Port))

	// ─── Graceful Shutdown ───────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutdown signal received — draining queues (max 30s)...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		log.Error("http shutdown error", zap.Error(err))
	}

	for _, jid := range waManager.ListSessions() {
		waManager.Disconnect(jid)
	}

	log.Info("connector stopped gracefully")
}

// buildMessageHandler cria o handler que converte eventos WhatsApp em InboundJobs.
func buildMessageHandler(
	ctx context.Context,
	q *queue.Queue,
	repo *db.Repository,
	waManager *whatsapp.Manager,
	metrics *telemetry.Metrics,
	log *zap.Logger,
) whatsapp.MessageHandler {
	return func(sessionID uuid.UUID, sessionJID string, evt *events.Message) {
		log.Info("onMsg handler called",
			zap.String("session_jid", sessionJID),
			zap.String("from", evt.Info.Sender.String()),
			zap.Bool("from_me", evt.Info.IsFromMe),
			zap.Bool("is_group", evt.Info.IsGroup),
			zap.String("msg_id", evt.Info.ID),
		)
		if evt.Info.IsFromMe {
			return
		}

		text := ""
		msgType := db.MsgTypeText
		var mediaData []byte
		var mediaName, mediaMime string

		if evt.Message.GetConversation() != "" {
			text = evt.Message.GetConversation()
		} else if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
			text = ext.GetText()
		} else if img := evt.Message.GetImageMessage(); img != nil {
			msgType = db.MsgTypeImage
			text = img.GetCaption()
			mediaMime = img.GetMimetype()
			mediaName = "image.jpg"
			if data, err := waManager.DownloadMedia(sessionJID, img); err == nil {
				mediaData = data
			} else {
				log.Warn("download image failed", zap.Error(err))
			}
		} else if aud := evt.Message.GetAudioMessage(); aud != nil {
			msgType = db.MsgTypeAudio
			mediaMime = aud.GetMimetype()
			mediaName = "audio.ogg"
			if aud.GetPTT() {
				mediaName = "voice.ogg"
			}
			if data, err := waManager.DownloadMediaFromMessage(sessionJID, evt.Message, aud); err == nil {
				mediaData = data
			} else {
				log.Warn("download audio failed", zap.Error(err))
				text = "[Áudio]"
			}
		} else if doc := evt.Message.GetDocumentMessage(); doc != nil {
			msgType = db.MsgTypeDocument
			text = doc.GetFileName()
			mediaMime = doc.GetMimetype()
			mediaName = doc.GetFileName()
			if mediaName == "" {
				mediaName = "document"
			}
			if data, err := waManager.DownloadMedia(sessionJID, doc); err == nil {
				mediaData = data
			} else {
				log.Warn("download document failed", zap.Error(err))
			}
		} else if vid := evt.Message.GetVideoMessage(); vid != nil {
			msgType = db.MsgTypeVideo
			text = vid.GetCaption()
			mediaMime = vid.GetMimetype()
			mediaName = "video.mp4"
			if data, err := waManager.DownloadMedia(sessionJID, vid); err == nil {
				mediaData = data
			} else {
				log.Warn("download video failed", zap.Error(err))
			}
		} else if contact := evt.Message.GetContactMessage(); contact != nil {
			msgType = db.MsgTypeDocument
			mediaName = contact.GetDisplayName() + ".vcf"
			if mediaName == ".vcf" {
				mediaName = "contato.vcf"
			}
			mediaMime = "text/vcard"
			vcard := contact.GetVcard()
			if vcard != "" {
				mediaData = []byte(vcard)
			} else {
				text = "[Contato: " + contact.GetDisplayName() + "]"
			}
		} else if sticker := evt.Message.GetStickerMessage(); sticker != nil {
			msgType = db.MsgTypeImage
			mediaMime = sticker.GetMimetype()
			mediaName = "sticker.webp"
			if data, err := waManager.DownloadMedia(sessionJID, sticker); err == nil {
				mediaData = data
			} else {
				log.Warn("download sticker failed", zap.Error(err))
				text = "[Sticker]"
			}
		}

		// Salva mensagem no banco com status "received"
		ts := evt.Info.Timestamp
		msg := &db.Message{
			ID:          uuid.New(),
			WAMessageID: evt.Info.ID,
			SessionID:   &sessionID,
			Direction:   db.DirInbound,
			MessageType: msgType,
			Content:     text,
			MediaMime:   mediaMime,
			Status:      db.MsgReceived,
			SentAt:      &ts,
		}
		if err := repo.InsertMessage(ctx, msg); err != nil {
			log.Warn("insert message failed", zap.String("msg_id", evt.Info.ID), zap.Error(err))
		}

		job := &queue.InboundJob{
			SessionID:   sessionID,
			SessionJID:  sessionJID,
			FromJID:     evt.Info.Sender.String(),
			FromPhone:   evt.Info.Sender.User,
			FromName:    evt.Info.PushName,
			MessageID:   evt.Info.ID,
			MessageType: string(msgType),
			Text:        text,
			MediaData:   mediaData,
			MediaName:   mediaName,
			MediaMime:   mediaMime,
		}

		if err := q.PushInbound(ctx, job); err != nil {
			log.Error("push inbound failed", zap.String("msg_id", evt.Info.ID), zap.Error(err))
			_ = repo.UpdateMessageStatus(ctx, evt.Info.ID, db.MsgFailed, err.Error())
			return
		}

		log.Info("message queued", zap.String("from", job.FromPhone), zap.String("type", string(msgType)))
	}
}

// downloadURL faz GET em uma URL e retorna o corpo como bytes.
func downloadURL(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
