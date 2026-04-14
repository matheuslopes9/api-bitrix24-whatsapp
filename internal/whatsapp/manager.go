package whatsapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// DownloadMedia baixa bytes de mídia de uma mensagem WhatsApp.
// msg deve implementar whatsmeow.DownloadableMessage.
func (m *Manager) DownloadMedia(sessionJID string, msg whatsmeow.DownloadableMessage) ([]byte, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionJID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionJID)
	}
	return sess.Client.Download(context.Background(), msg)
}

// DownloadMediaFromMessage baixa mídia usando a mensagem completa.
// Para áudio com HMAC inválido, tenta também com MediaDocument como fallback
// (chave HKDF diferente — às vezes resolve quando a MediaKey está incorreta para audio).
func (m *Manager) DownloadMediaFromMessage(sessionJID string, fullMsg *waProto.Message, primary whatsmeow.DownloadableMessage) ([]byte, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionJID]
	m.mu.RUnlock()
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
			-1,
			whatsmeow.MediaDocument,
			"",
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

// LoadAll carrega todas as sessões do banco (ativas e desconectadas) e reconecta.
func (m *Manager) LoadAll(ctx context.Context) error {
	sessions, err := m.repo.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	connected := 0
	for _, s := range sessions {
		s := s // capture
		if err := m.connectSession(ctx, &s); err != nil {
			m.log.Warn("failed to reconnect session", zap.String("jid", s.JID), zap.Error(err))
			_ = m.repo.UpdateSessionStatus(ctx, s.JID, db.SessionDisconnected)
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
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+dbPath+"?_foreign_keys=on", waLog.Noop)
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

// SendTyping envia o indicador de "digitando..." para o contato e para automaticamente
// quando a mensagem for enviada. Deve ser chamado logo antes de SendMessage/SendDocument/SendAudio.
func (m *Manager) SendTyping(ctx context.Context, sessionJID, toJID string, duration time.Duration) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionJID]
	m.mu.RUnlock()
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

// Send envia uma mensagem de texto.
func (m *Manager) Send(ctx context.Context, sessionJID, toJID, text string) (string, error) {
	m.mu.RLock()
	sess, ok := m.sessions[sessionJID]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := types.ParseJID(toJID)
	if err != nil {
		return "", fmt.Errorf("invalid jid: %w", err)
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
	m.mu.RLock()
	sess, ok := m.sessions[sessionJID]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := types.ParseJID(toJID)
	if err != nil {
		return "", fmt.Errorf("invalid jid: %w", err)
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
	m.mu.RLock()
	sess, ok := m.sessions[sessionJID]
	m.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("session not found: %s", sessionJID)
	}

	recipient, err := types.ParseJID(toJID)
	if err != nil {
		return "", fmt.Errorf("invalid jid: %w", err)
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

// Disconnect faz logout completo: revoga o dispositivo no WhatsApp,
// remove do banco de dados e apaga o arquivo SQLite da sessão.
func (m *Manager) Disconnect(jid string) {
	m.mu.Lock()
	sess, ok := m.sessions[jid]
	if ok {
		delete(m.sessions, jid)
	}
	m.mu.Unlock()

	if !ok {
		return
	}

	ctx := context.Background()

	// Logout revoga o dispositivo no app do celular
	if err := sess.Client.Logout(ctx); err != nil {
		m.log.Warn("logout error (ignoring)", zap.String("jid", jid), zap.Error(err))
	}
	sess.Client.Disconnect()

	// Remove do banco de dados
	if err := m.repo.DeleteSession(ctx, jid); err != nil {
		m.log.Warn("delete session from db failed", zap.String("jid", jid), zap.Error(err))
	}

	// Remove o arquivo SQLite da sessão
	if sess.dbPath != "" {
		if err := os.Remove(sess.dbPath); err != nil && !os.IsNotExist(err) {
			m.log.Warn("remove session sqlite failed", zap.String("path", sess.dbPath), zap.Error(err))
		}
	}

	m.log.Info("session disconnected and removed", zap.String("jid", jid))
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

	container, err := sqlstore.New(ctx, "sqlite3", "file:"+s.SessionFile+"?_foreign_keys=on", waLog.Noop)
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
			m.log.Warn("session logged out", zap.String("jid", sess.JID))
			_ = m.repo.UpdateSessionStatus(context.Background(), sess.JID, db.SessionBanned)
			m.mu.Lock()
			delete(m.sessions, sess.JID)
			m.mu.Unlock()
		}
	}
}
