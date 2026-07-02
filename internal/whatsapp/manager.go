package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mdp/qrterminal/v3"
	_ "github.com/mattn/go-sqlite3"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/config"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.uber.org/zap"
)

// MessageHandler é a função chamada para cada mensagem recebida.
type MessageHandler func(sessionID uuid.UUID, jid string, evt *events.Message)

// Manager gerencia múltiplas sessões WhatsApp simultaneamente.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	qrCodes  map[string]string // phone -> QR code atual
	cfg      *config.WhatsAppConfig
	repo     *db.Repository
	log      *zap.Logger
	onMsg    MessageHandler
}

func NewManager(cfg *config.WhatsAppConfig, repo *db.Repository, log *zap.Logger, onMsg MessageHandler) *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
		qrCodes:  make(map[string]string),
		cfg:      cfg,
		repo:     repo,
		log:      log,
		onMsg:    onMsg,
	}
}

// SetMessageHandler define o handler de mensagens após a criação do Manager.
func (m *Manager) SetMessageHandler(h MessageHandler) {
	m.onMsg = h
}

// SessionsDir retorna o diretório de session files (uso de diagnostico).
func (m *Manager) SessionsDir() string {
	return m.cfg.SessionsDir
}

// CleanupSessionFilesForPhones remove .db/.db-shm/.db-wal de telefones
// especificos. Util para limpar dados de um tenant especifico (deletar
// arquivos de um cliente que está sendo desinstalado, ou para
// resetar a sessão QR de um cliente sem afetar outros).
//
// Diferente de CleanupOrphanSessionFiles, este metodo apaga TUDO dos
// phones listados (mesmo se a sessão estiver active). Caller deve
// confirmar com o usuario antes.
func (m *Manager) CleanupSessionFilesForPhones(phones []string) (removed []string, bytesFreed int64, err error) {
	if len(phones) == 0 {
		return nil, 0, nil
	}
	dir := m.cfg.SessionsDir
	phoneSet := map[string]bool{}
	for _, p := range phones {
		phoneSet[p] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var phone string
		switch {
		case strings.HasSuffix(name, ".db"):
			phone = strings.TrimSuffix(name, ".db")
		case strings.HasSuffix(name, ".db-shm"):
			phone = strings.TrimSuffix(name, ".db-shm")
		case strings.HasSuffix(name, ".db-wal"):
			phone = strings.TrimSuffix(name, ".db-wal")
		default:
			continue
		}
		if !phoneSet[phone] {
			continue
		}
		if info, _ := e.Info(); info != nil {
			bytesFreed += info.Size()
		}
		path := filepath.Join(dir, name)
		if rerr := os.Remove(path); rerr == nil {
			removed = append(removed, name)
		}
	}
	return removed, bytesFreed, nil
}

// CleanupOrphanSessionFiles remove .db/.db-shm/.db-wal de:
//   - sessoes Cloud API (que NUNCA deveriam ter session file)
//   - .db sem sessao ativa correspondente no banco
//   - .db-shm/.db-wal sem .db principal (sidecars orfaos)
//
// Mantem so o que e necessario para sessoes QR ativas. Retorna lista de
// arquivos removidos e tamanho total liberado.
func (m *Manager) CleanupOrphanSessionFiles(ctx context.Context) (removed []string, bytesFreed int64, err error) {
	dir := m.cfg.SessionsDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}

	// Lista phones de sessoes QR ativas (estes a gente preserva)
	allSessions, err := m.repo.ListAllSessions(ctx)
	if err != nil {
		return nil, 0, err
	}
	activeQRPhones := map[string]bool{}
	for _, s := range allSessions {
		if s.Type != db.SessionTypeCloudAPI && s.Status == db.SessionActive {
			activeQRPhones[s.Phone] = true
		}
	}

	// Indexa todos os .db existentes para detectar sidecars orfaos
	existingDB := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".db") {
			existingDB[strings.TrimSuffix(name, ".db")] = true
		}
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var phone string
		switch {
		case strings.HasSuffix(name, ".db"):
			phone = strings.TrimSuffix(name, ".db")
		case strings.HasSuffix(name, ".db-shm"):
			phone = strings.TrimSuffix(name, ".db-shm")
		case strings.HasSuffix(name, ".db-wal"):
			phone = strings.TrimSuffix(name, ".db-wal")
		default:
			continue
		}

		// sidecar orfao (sem .db principal) — sempre remove
		isSidecar := strings.HasSuffix(name, ".db-shm") || strings.HasSuffix(name, ".db-wal")
		if isSidecar && !existingDB[phone] {
			if info, _ := e.Info(); info != nil {
				bytesFreed += info.Size()
			}
			path := filepath.Join(dir, name)
			if rerr := os.Remove(path); rerr == nil {
				removed = append(removed, name)
			}
			continue
		}

		// se phone nao bate com nenhuma sessao QR ativa, remove
		if !activeQRPhones[phone] {
			if info, _ := e.Info(); info != nil {
				bytesFreed += info.Size()
			}
			path := filepath.Join(dir, name)
			if rerr := os.Remove(path); rerr == nil {
				removed = append(removed, name)
			}
		}
	}
	return removed, bytesFreed, nil
}

// DownloadMedia baixa bytes de mídia de uma mensagem WhatsApp.
// msg deve implementar whatsmeow.DownloadableMessage.
func (m *Manager) DownloadMedia(sessionJID string, msg whatsmeow.DownloadableMessage) ([]byte, error) {
	// resolveSession faz match por telefone (ignora device suffix ":NN" que
	// muda a cada reconexao). Lookup direto m.sessions[sessionJID] falhava
	// quando o suffix do JID do evento divergia do suffix usado como chave
	// no map — resultando em "session not found" e download falhando
	// (foto/sticker viravam fallback de texto).
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionJID)
	}
	return sess.Client.Download(context.Background(), msg)
}

// DownloadMediaFromMessage baixa mídia usando a mensagem completa.
// Para áudio com HMAC inválido, tenta também com MediaDocument como fallback
// (chave HKDF diferente — às vezes resolve quando a MediaKey está incorreta para audio).
func (m *Manager) DownloadMediaFromMessage(sessionJID string, fullMsg *waProto.Message, primary whatsmeow.DownloadableMessage) ([]byte, error) {
	// resolveSession por telefone — ver comentario em DownloadMedia.
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionJID)
	}
	data, err := sess.Client.Download(context.Background(), primary)
	if err == nil {
		return data, nil
	}
	// Fallback 1: tenta DownloadAny (testa todos os tipos de mídia)
	if data2, err2 := sess.Client.DownloadAny(context.Background(), fullMsg); err2 == nil {
		return data2, nil
	}
	// Fallback 2: tenta baixar diretamente pelo DirectPath com MediaDocument
	// Útil quando o AudioMessage tem MediaKey derivada com tipo errado
	if aud := fullMsg.GetAudioMessage(); aud != nil && len(aud.GetDirectPath()) > 0 {
		data3, err3 := sess.Client.DownloadMediaWithPath(
			context.Background(),
			aud.GetDirectPath(),
			aud.GetFileEncSHA256(),
			aud.GetFileSHA256(),
			aud.GetMediaKey(),
			whatsmeow.MediaDocument, // mediaType (assinatura nova whatsmeow jun/2026)
			"",                      // mmsType (vazio = deriva do mediaType)
			false,                   // allowNoHash
		)
		if err3 == nil {
			return data3, nil
		}
	}
	return nil, err
}

// GetQR retorna o QR code atual para um telefone (vazio se não disponível).
func (m *Manager) GetQR(phone string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.qrCodes[phone]
}

// LoadAll carrega todas as sessões QR do banco (ativas e desconectadas) e reconecta.
// Pula sessões Cloud API — essas são gerenciadas pelo CloudManager (HTTPS stateless,
// não precisam de WebSocket whatsmeow nem session file SQLite).
func (m *Manager) LoadAll(ctx context.Context) error {
	sessions, err := m.repo.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	connected := 0
	for _, s := range sessions {
		// Skip Cloud — não tem session file e não usa whatsmeow.
		if s.Type == db.SessionTypeCloudAPI {
			continue
		}
		s := s // capture
		if err := m.connectSession(ctx, &s); err != nil {
			m.log.Warn("failed to reconnect session", zap.String("jid", s.JID), zap.Error(err))
			_ = m.repo.UpdateSessionStatus(ctx, s.JID, db.SessionDisconnected)
			// Se o arquivo SQLite sumiu (volume remontado, rebuild etc.), recria o arquivo
			// via AddSession para que o usuário possa escanear o QR novamente.
			if strings.Contains(err.Error(), "session file not found") {
				m.log.Info("session file missing — reinitializing to allow QR rescan", zap.String("phone", s.Phone))
				go m.initSession(s.Phone, filepath.Join(m.cfg.SessionsDir, s.Phone+".db"))
			}
		} else {
			connected++
		}
	}
	m.log.Info("sessions loaded", zap.Int("count", connected))
	return nil
}

// AddSession inicia conexão WhatsApp em background. Retorna imediatamente.
// Se for nova sessão, o QR fica disponível via GetQR(phone) após alguns segundos.
func (m *Manager) AddSession(_ context.Context, phone string) error {
	dbPath := filepath.Join(m.cfg.SessionsDir, phone+".db")
	if err := os.MkdirAll(m.cfg.SessionsDir, 0o755); err != nil {
		return err
	}
	// Tudo em background — nunca bloqueia a request HTTP
	go m.initSession(phone, dbPath)
	return nil
}

func (m *Manager) initSession(phone, dbPath string) {
	ctx := context.Background()
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+dbPath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000", waLog.Noop)
	if err != nil {
		m.log.Error("open sqlite store", zap.String("phone", phone), zap.Error(err))
		return
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		m.log.Error("get device", zap.String("phone", phone), zap.Error(err))
		return
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Noop)

	if client.Store.ID == nil {
		// Nova sessão — gera QR
		m.connectWithQR(ctx, phone, dbPath, client)
		return
	}

	// Sessão já existe — reconecta
	if err := client.Connect(); err != nil {
		m.log.Error("reconnect error", zap.String("phone", phone), zap.Error(err))
		return
	}
	jid := client.Store.ID.String()
	sessionID := uuid.New()
	sess := &Session{ID: sessionID, JID: jid, Phone: phone, Client: client, dbPath: dbPath}
	client.AddEventHandler(m.buildEventHandler(sess))
	m.mu.Lock()
	m.sessions[jid] = sess
	m.mu.Unlock()
	_ = m.repo.UpsertSession(ctx, &db.WhatsAppSession{
		ID: sessionID, JID: jid, Phone: phone,
		Status: db.SessionActive, SessionFile: dbPath,
	})
	m.log.Info("session reconnected", zap.String("jid", jid))
}

// connectWithQR estabelece conexão nova com geração de QR via event handler.
func (m *Manager) connectWithQR(ctx context.Context, phone, dbPath string, client *whatsmeow.Client) {
	// Usa event handler direto — mais confiável que GetQRChannel
	client.AddEventHandler(func(rawEvt interface{}) {
		m.log.Info("raw whatsapp event", zap.String("type", fmt.Sprintf("%T", rawEvt)), zap.String("phone", phone))
		switch evt := rawEvt.(type) {
		case *events.QR:
			// QR chegou — salva o primeiro código
			if len(evt.Codes) > 0 {
				code := evt.Codes[0]
				qrterminal.GenerateHalfBlock(code, qrterminal.L, os.Stdout)
				m.mu.Lock()
				m.qrCodes[phone] = code
				m.mu.Unlock()
				m.log.Info("qr code ready via event", zap.String("phone", phone))
			}
		case *events.PairSuccess:
			m.mu.Lock()
			delete(m.qrCodes, phone)
			m.mu.Unlock()
			jid := evt.ID.String()
			sessionID := uuid.New()
			sess := &Session{ID: sessionID, JID: jid, Phone: phone, Client: client, dbPath: dbPath}
			m.mu.Lock()
			m.sessions[jid] = sess
			m.mu.Unlock()
			_ = m.repo.UpsertSession(context.Background(), &db.WhatsAppSession{
				ID: sessionID, JID: jid, Phone: phone,
				Status: db.SessionActive, SessionFile: dbPath,
			})
			m.log.Info("session paired via qr", zap.String("jid", jid), zap.String("phone", phone))
			// AddEventHandler fora do handler atual para evitar deadlock no whatsmeow
			go client.AddEventHandler(m.buildEventHandler(sess))
		case *events.Connected:
			jid := client.Store.ID.String()
			_ = m.repo.UpdateSessionStatus(context.Background(), jid, db.SessionActive)
			m.log.Info("session connected after scan", zap.String("jid", jid))
		}
	})

	if err := client.Connect(); err != nil {
		m.log.Error("connect error", zap.String("phone", phone), zap.Error(err))
		return
	}
	m.log.Info("whatsapp connect started", zap.String("phone", phone))
}

// TypingDelay calcula o tempo de digitação simulado com base no texto.
// Mínimo 1.5s, máximo 4s, com jitter de ±25% para parecer humano.
func (m *Manager) TypingDelay(text string) time.Duration {
	chars := len([]rune(text))
	if chars > 120 {
		chars = 120
	}
	ms := 1500 + chars*20 // ~300 chars/min
	if ms > 4000 {
		ms = 4000
	}
	// jitter ±25%
	jitter := int(float64(ms) * 0.25)
	if jitter > 0 {
		// determinístico baseado no conteúdo para evitar import de math/rand no manager
		h := 0
		for _, c := range text {
			h = h*31 + int(c)
		}
		if h < 0 {
			h = -h
		}
		ms += (h % (2*jitter + 1)) - jitter
	}
	return time.Duration(ms) * time.Millisecond
}

// resolveSession encontra a sessão pelo JID exato. Se não encontrar, tenta por prefixo
// de número de telefone — necessário quando o JID do banco (ex: :19) difere do JID real
// da sessão em memória após reconexão (ex: :44).
func (m *Manager) resolveSession(sessionJID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if sess, ok := m.sessions[sessionJID]; ok {
		return sess, true
	}
	// Extrai o número base: "5519910001772:19@s.whatsapp.net" → "5519910001772"
	phone := sessionJID
	if at := strings.Index(phone, "@"); at > 0 {
		phone = phone[:at]
	}
	if colon := strings.Index(phone, ":"); colon > 0 {
		phone = phone[:colon]
	}
	if phone == "" {
		return nil, false
	}
	for jid, sess := range m.sessions {
		base := jid
		if at := strings.Index(base, "@"); at > 0 {
			base = base[:at]
		}
		if colon := strings.Index(base, ":"); colon > 0 {
			base = base[:colon]
		}
		if base == phone {
			return sess, true
		}
	}
	return nil, false
}

// GroupInfo busca metadados do grupo (nome, participantes, etc) pelo JID.
// Usado pra resolver "nome do grupo" quando mensagem chega de grupo
// WhatsApp e queremos mostrar como chat no Bitrix Open Channel.
//
// Best-effort: se nao conseguir resolver, retorna (nil, err) e o caller
// usa fallback ("Grupo <jid>"). Nao cacheia — whatsmeow ja cacheia
// internamente em sessao.
func (m *Manager) GroupInfo(ctx context.Context, sessionJID string, groupJID types.JID) (*types.GroupInfo, error) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok || sess.Client == nil {
		return nil, fmt.Errorf("session not found: %s", sessionJID)
	}
	return sess.Client.GetGroupInfo(ctx, groupJID)
}

// SendTyping envia o indicador de "digitando..." para o contato e para automaticamente
// quando a mensagem for enviada. Deve ser chamado logo antes de SendMessage/SendDocument/SendAudio.
func (m *Manager) SendTyping(ctx context.Context, sessionJID, toJID string, duration time.Duration) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return
	}
	recipient, err := types.ParseJID(toJID)
	if err != nil {
		return
	}
	_ = sess.Client.SendChatPresence(ctx, recipient, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	select {
	case <-time.After(duration):
	case <-ctx.Done():
	}
	_ = sess.Client.SendChatPresence(ctx, recipient, types.ChatPresencePaused, types.ChatPresenceMediaText)
}

// resolveRecipient converte um toJID em types.JID pronto pra envio,
// resolvendo o JID CANONICO quando for numero de telefone (@s.whatsapp.net).
//
// Motivacao: numeros BR antigos sem o "9" do celular (ex: 559691459459)
// existem no WhatsApp mas com um JID interno que pode divergir do numero
// digitado. Mandar direto pra "559691459459@s.whatsapp.net" as vezes
// retorna erro 400 porque o WhatsApp espera o JID canonico registrado.
//
// IsOnWhatsApp pergunta ao servidor qual e' o JID real do numero. Se o
// numero existe, usa o JID retornado. Se nao existe/falha a consulta,
// cai no JID original (best-effort — nao bloqueia envio se a consulta
// de verificacao falhar por rede).
//
// Grupos (@g.us) e LID (@lid) passam direto — nao precisam resolucao.
func (m *Manager) resolveRecipient(ctx context.Context, sess *Session, toJID string) (types.JID, error) {
	jid, err := types.ParseJID(toJID)
	if err != nil {
		return types.JID{}, fmt.Errorf("invalid jid: %w", err)
	}
	// So' resolve numeros de telefone. Grupos e LID passam direto.
	if jid.Server != types.DefaultUserServer {
		return jid, nil
	}

	// Otimizacao: so' consulta IsOnWhatsApp (chamada de rede) pra numeros
	// que PODEM ter o problema do "9" — numeros BR (55) com 12 digitos
	// (DDD 2 + 8 do celular antigo, sem o 9). Numeros normais (13 digitos
	// BR, ou qualquer outro pais) passam direto sem custo de rede.
	needsResolve := len(jid.User) == 12 && strings.HasPrefix(jid.User, "55")
	if !needsResolve {
		return jid, nil
	}

	phone := "+" + jid.User
	results, qErr := sess.Client.IsOnWhatsApp(ctx, []string{phone})
	if qErr != nil || len(results) == 0 {
		m.log.Warn("resolveRecipient: IsOnWhatsApp falhou, usando JID original",
			zap.String("phone", phone), zap.Error(qErr))
		return jid, nil
	}
	r := results[0]
	if !r.IsIn {
		return types.JID{}, fmt.Errorf("numero %s nao esta no WhatsApp", phone)
	}
	target := jid
	if !r.JID.IsEmpty() {
		target = r.JID
		m.log.Info("resolveRecipient: JID canonico resolvido",
			zap.String("input", jid.User), zap.String("canonical", r.JID.User))
	}

	// Aquece o mapeamento PN->LID + prekeys do destinatario. Numeros BR
	// antigos sem o 9 costumam falhar com erro 400 no envio quando o
	// whatsmeow nao tem os devices/LID resolvidos ainda. GetUserDevices
	// forca essa resolucao e popula o store. Best-effort: erro aqui nao
	// bloqueia (o envio ainda tenta com o JID que temos).
	if devices, devErr := sess.Client.GetUserDevices(ctx, []types.JID{target}); devErr != nil {
		m.log.Warn("resolveRecipient: GetUserDevices falhou (segue mesmo assim)",
			zap.String("jid", target.String()), zap.Error(devErr))
	} else {
		m.log.Info("resolveRecipient: devices resolvidos",
			zap.String("jid", target.String()), zap.Int("device_count", len(devices)))
	}

	return target, nil
}

// Send envia uma mensagem de texto.
func (m *Manager) Send(ctx context.Context, sessionJID, toJID, text string) (string, error) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := m.resolveRecipient(ctx, sess, toJID)
	if err != nil {
		return "", err
	}

	resp, err := sess.Client.SendMessage(ctx, recipient, &waProto.Message{
		Conversation: &text,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendAudio envia um arquivo de áudio no WhatsApp como mensagem de áudio reproduzível inline.
// ptt=true faz aparecer como voice note com botão de play; ptt=false como áudio normal.
func (m *Manager) SendAudio(ctx context.Context, sessionJID, toJID string, data []byte, mime string, ptt bool) (string, error) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := m.resolveRecipient(ctx, sess, toJID)
	if err != nil {
		return "", err
	}

	uploaded, err := sess.Client.Upload(ctx, data, whatsmeow.MediaAudio)
	if err != nil {
		return "", fmt.Errorf("upload audio: %w", err)
	}

	seconds := uint32(0) // duração desconhecida
	msg := &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			Mimetype:      &mime,
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    &uploaded.FileLength,
			Seconds:       &seconds,
			PTT:           &ptt,
		},
	}

	resp, err := sess.Client.SendMessage(ctx, recipient, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendDocument envia um arquivo como documento no WhatsApp e retorna o WA message ID.
func (m *Manager) SendDocument(ctx context.Context, sessionJID, toJID string, data []byte, mime, fileName string) (string, error) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := m.resolveRecipient(ctx, sess, toJID)
	if err != nil {
		return "", err
	}

	uploaded, err := sess.Client.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return "", fmt.Errorf("upload document: %w", err)
	}

	msg := &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			FileName:      &fileName,
			Mimetype:      &mime,
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    &uploaded.FileLength,
		},
	}

	resp, err := sess.Client.SendMessage(ctx, recipient, msg)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendImage envia uma imagem inline (aparece como foto no WhatsApp, nao
// como anexo de arquivo) com legenda opcional. Usado quando o atendente
// anexa imagem pelo Bitrix — SendDocument mandava como arquivo e perdia
// a legenda.
func (m *Manager) SendImage(ctx context.Context, sessionJID, toJID string, data []byte, mime, caption string) (string, error) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := m.resolveRecipient(ctx, sess, toJID)
	if err != nil {
		return "", err
	}

	uploaded, err := sess.Client.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return "", fmt.Errorf("upload image: %w", err)
	}

	imgMsg := &waProto.ImageMessage{
		Mimetype:      &mime,
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}
	if caption != "" {
		imgMsg.Caption = &caption
	}

	resp, err := sess.Client.SendMessage(ctx, recipient, &waProto.Message{ImageMessage: imgMsg})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// SendVideo envia video inline com legenda opcional. Espelha SendImage.
func (m *Manager) SendVideo(ctx context.Context, sessionJID, toJID string, data []byte, mime, caption string) (string, error) {
	sess, ok := m.resolveSession(sessionJID)
	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := m.resolveRecipient(ctx, sess, toJID)
	if err != nil {
		return "", err
	}

	uploaded, err := sess.Client.Upload(ctx, data, whatsmeow.MediaVideo)
	if err != nil {
		return "", fmt.Errorf("upload video: %w", err)
	}

	vidMsg := &waProto.VideoMessage{
		Mimetype:      &mime,
		URL:           &uploaded.URL,
		DirectPath:    &uploaded.DirectPath,
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    &uploaded.FileLength,
	}
	if caption != "" {
		vidMsg.Caption = &caption
	}

	resp, err := sess.Client.SendMessage(ctx, recipient, &waProto.Message{VideoMessage: vidMsg})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

// Disconnect faz logout completo: revoga o dispositivo no WhatsApp,
// remove TODAS as rows de whatsapp_sessions com o mesmo phone (independente
// do device suffix) e apaga TODOS os arquivos .db/.db-shm/.db-wal daquele
// phone — sem deixar lixo que cause sessao re-aparecer no proximo deploy.
func (m *Manager) Disconnect(jid string) {
	m.mu.Lock()
	sess, ok := m.sessions[jid]
	if ok {
		delete(m.sessions, jid)
	}
	m.mu.Unlock()

	ctx := context.Background()

	// Se a sessao estava em memoria, faz logout no whatsapp + disconnect do client
	if ok && sess != nil {
		if err := sess.Client.Logout(ctx); err != nil {
			m.log.Warn("logout error (ignoring)", zap.String("jid", jid), zap.Error(err))
		}
		sess.Client.Disconnect()
	}

	// Extrai o phone (numero sem device suffix) do JID
	phone := extractPhoneFromJID(jid)

	// Remove TODAS as rows whatsapp_sessions com mesmo phone (ou jid exato como fallback).
	// Sem isso, devices antigos (jid:88, jid:89) sobrevivem e re-aparecem.
	if phone != "" {
		if err := m.repo.DeleteSessionsByPhone(ctx, phone); err != nil {
			m.log.Warn("delete sessions by phone failed",
				zap.String("phone", phone), zap.Error(err))
		}
	} else {
		// Fallback: pelo menos apaga a row exata do JID
		if err := m.repo.DeleteSession(ctx, jid); err != nil {
			m.log.Warn("delete session from db failed", zap.String("jid", jid), zap.Error(err))
		}
	}

	// Apaga TODOS os arquivos .db/.db-shm/.db-wal do phone, nao so o
	// sess.dbPath em memoria — devices antigos deixavam sidecars.
	if phone != "" {
		base := filepath.Join(m.cfg.SessionsDir, phone)
		for _, suffix := range []string{".db", ".db-shm", ".db-wal"} {
			p := base + suffix
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				m.log.Warn("remove session file failed", zap.String("path", p), zap.Error(err))
			}
		}
	} else if ok && sess != nil && sess.dbPath != "" {
		// Fallback: apaga so o dbPath conhecido
		if err := os.Remove(sess.dbPath); err != nil && !os.IsNotExist(err) {
			m.log.Warn("remove session sqlite failed", zap.String("path", sess.dbPath), zap.Error(err))
		}
	}

	m.log.Info("session disconnected and removed",
		zap.String("jid", jid), zap.String("phone", phone))
}

// extractPhoneFromJID retorna o numero sem device suffix.
// "5519910001772:88@s.whatsapp.net" -> "5519910001772"
// "cloud:1160@s.whatsapp.net" -> "" (Cloud, nao tem .db file)
func extractPhoneFromJID(jid string) string {
	if strings.HasPrefix(jid, "cloud:") {
		return ""
	}
	at := strings.Index(jid, "@")
	if at == -1 {
		return ""
	}
	base := jid[:at]
	if colon := strings.Index(base, ":"); colon != -1 {
		base = base[:colon]
	}
	// Mantem so digitos (sanity)
	out := make([]byte, 0, len(base))
	for _, c := range base {
		if c >= '0' && c <= '9' {
			out = append(out, byte(c))
		}
	}
	return string(out)
}

// Ping verifica se a conexão está ativa.
func (m *Manager) Ping(jid string) bool {
	m.mu.RLock()
	sess, ok := m.sessions[jid]
	m.mu.RUnlock()
	return ok && sess.Client.IsConnected()
}

// Reconnect tenta reconectar uma sessão que estava desconectada.
func (m *Manager) Reconnect(ctx context.Context, s *db.WhatsAppSession) error {
	// Se já está no mapa (mesmo que ainda conectando), não interfere
	m.mu.RLock()
	_, exists := m.sessions[s.JID]
	m.mu.RUnlock()
	if exists {
		return nil
	}
	return m.connectSession(ctx, s)
}

// ListSessions retorna todos os JIDs ativos.
func (m *Manager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.sessions))
	for k := range m.sessions {
		keys = append(keys, k)
	}
	return keys
}

func (m *Manager) connectSession(ctx context.Context, s *db.WhatsAppSession) error {
	// Verifica se o arquivo SQLite existe antes de tentar conectar
	if _, err := os.Stat(s.SessionFile); os.IsNotExist(err) {
		return fmt.Errorf("session file not found: %s", s.SessionFile)
	}

	container, err := sqlstore.New(ctx, "sqlite3", "file:"+s.SessionFile+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000", waLog.Noop)
	if err != nil {
		return err
	}
	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return err
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Noop)

	// Usa o JID real do device (pode diferir do JID salvo no banco após reconexão).
	// Isso garante que m.sessions sempre usa a chave correta.
	realJID := s.JID
	if client.Store.ID != nil {
		realJID = client.Store.ID.String()
	}

	sess := &Session{
		ID:     s.ID,
		JID:    realJID,
		Phone:  s.Phone,
		Client: client,
		dbPath: s.SessionFile,
	}

	client.AddEventHandler(m.buildEventHandler(sess))

	if err := client.Connect(); err != nil {
		return err
	}

	m.mu.Lock()
	m.sessions[realJID] = sess
	m.mu.Unlock()

	now := time.Now()
	_ = m.repo.UpsertSession(ctx, &db.WhatsAppSession{
		ID:          s.ID,
		JID:         realJID,
		Phone:       s.Phone,
		Status:      db.SessionActive,
		SessionFile: s.SessionFile,
		LastSeen:    &now,
	})

	if realJID != s.JID {
		m.log.Info("session jid updated on reconnect",
			zap.String("old_jid", s.JID),
			zap.String("new_jid", realJID),
			zap.String("phone", s.Phone))
	}

	return nil
}

func (m *Manager) buildEventHandler(sess *Session) func(interface{}) {
	return func(rawEvt interface{}) {
		switch evt := rawEvt.(type) {
		case *events.Message:
			m.log.Info("message event received",
				zap.String("jid", sess.JID),
				zap.String("from", evt.Info.Sender.String()),
				zap.Bool("from_me", evt.Info.IsFromMe),
				zap.Bool("is_group", evt.Info.IsGroup),
			)
			if m.onMsg != nil {
				m.onMsg(sess.ID, sess.JID, evt)
			}
		case *events.Disconnected:
			m.log.Warn("session disconnected", zap.String("jid", sess.JID))
			_ = m.repo.UpdateSessionStatus(context.Background(), sess.JID, db.SessionDisconnected)
		case *events.Connected:
			m.log.Info("session reconnected", zap.String("jid", sess.JID))
			_ = m.repo.UpdateSessionStatus(context.Background(), sess.JID, db.SessionActive)
		case *events.LoggedOut:
			// LoggedOut e' disparado pelo whatsmeow em varios cenarios, nao
			// apenas banimento real do WhatsApp:
			//   - User removeu o device manualmente em Dispositivos Vinculados
			//   - WhatsApp expirou a sessao por inatividade
			//   - Reconexao apos restart com Noise handshake corrompido
			//   - Conta foi banida (raro)
			//
			// Marcar como Banned (estado terminal, ignorado pelo watchdog) e'
			// agressivo demais — perdiamos sessoes que poderiam ser
			// resgatadas via QR rescan. Em vez disso, marcamos Disconnected
			// e removemos o store SQLite pra forcar reauth limpo no proximo
			// AddSession/Reconnect. Watchdog tenta ressuscitar; se falhar
			// repetidamente, fica em Disconnected ate o user reescanear QR
			// pelo /dashboard.
			m.log.Warn("session logged out by WhatsApp — marking disconnected (will need QR rescan)",
				zap.String("jid", sess.JID))
			_ = m.repo.UpdateSessionStatus(context.Background(), sess.JID, db.SessionDisconnected)
			m.mu.Lock()
			delete(m.sessions, sess.JID)
			m.mu.Unlock()
			// Remove store SQLite corrompido pra evitar loop de reconexao
			// com state ruim. Se o user reescanear QR, AddSession recria.
			if sess.Phone != "" {
				dbPath := filepath.Join(m.cfg.SessionsDir, sess.Phone+".db")
				if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
					m.log.Warn("failed to remove stale session file",
						zap.String("path", dbPath), zap.Error(err))
				} else {
					// Tambem remove -shm e -wal do SQLite (WAL mode files)
					_ = os.Remove(dbPath + "-shm")
					_ = os.Remove(dbPath + "-wal")
					m.log.Info("removed stale session file after LoggedOut",
						zap.String("path", dbPath))
				}
			}
		}
	}
}
