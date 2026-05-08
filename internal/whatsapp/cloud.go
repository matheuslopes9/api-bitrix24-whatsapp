package whatsapp

// Cloud API (Meta) — segunda forma de conectar, em paralelo ao QR Code.
// Não toca em nada do Manager whatsmeow existente. Os fluxos de envio passam
// por CombinedSender que escolhe o caminho certo por sessionJID.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"go.uber.org/zap"
)

// ErrMediaTooLarge é retornado quando a mídia inbound excede o limite do
// WhatsApp Business API (100MB). Permite o webhook avisar o cliente sem
// criar a mensagem no Bitrix.
var ErrMediaTooLarge = errors.New("media too large")

// MaxInboundMediaBytes — limite de 100MB do WhatsApp Business API.
const MaxInboundMediaBytes = 100 * 1024 * 1024

// escapeQuotes escapa aspas e backslashes em valores de header HTTP
// (Content-Disposition filename) — replica o que mime/multipart faz internamente.
func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}

const (
	// JID sintético para sessões Cloud API. Formato: cloud:<phone_number_id>@s.whatsapp.net
	// Permite que o resto do sistema (BitrixAccount.session_jid, contact_mapping,
	// messages.from_jid/to_jid) continue funcionando sem distinguir o tipo.
	cloudJIDPrefix = "cloud:"
	// API version padrão. Pode ser sobrescrita via env.
	defaultCloudAPIVersion = "v20.0"
	defaultCloudAPIBaseURL = "https://graph.facebook.com"
)

// CloudSession representa uma sessão Cloud API ativa em memória.
type CloudSession struct {
	JID            string // cloud:<phone_id>@s.whatsapp.net
	PhoneNumberID  string
	WABAID         string
	AccessToken    string
	AppSecret      string
	VerifyToken    string
	DisplayPhone   string
	APIVersion     string
	BaseURL        string
	LastOK         time.Time
	mu             sync.RWMutex
}

// CloudManager gerencia sessões Cloud API. Coexiste com o Manager whatsmeow.
type CloudManager struct {
	mu       sync.RWMutex
	sessions map[string]*CloudSession // por sessionJID
	repo     *db.Repository
	http     *http.Client
	log      *zap.Logger
	apiVer   string
	baseURL  string
}

// NewCloudManager cria um manager de sessões Cloud API.
func NewCloudManager(repo *db.Repository, log *zap.Logger) *CloudManager {
	return &CloudManager{
		sessions: make(map[string]*CloudSession),
		repo:     repo,
		http:     &http.Client{Timeout: 30 * time.Second},
		log:      log,
		apiVer:   defaultCloudAPIVersion,
		baseURL:  defaultCloudAPIBaseURL,
	}
}

// IsCloudJID verifica se o JID corresponde a uma sessão Cloud API.
func IsCloudJID(jid string) bool {
	return strings.HasPrefix(jid, cloudJIDPrefix)
}

// CloudJIDFromPhoneID monta o JID sintético a partir do phone_number_id.
func CloudJIDFromPhoneID(phoneID string) string {
	return cloudJIDPrefix + phoneID + "@s.whatsapp.net"
}

// LoadAll carrega TODAS as sessões Cloud API do banco para a memória.
// Diferente do whatsmeow (que pode estar legitimamente desconectado),
// sessões Cloud API são stateless via HTTPS — enquanto o token estiver
// salvo no banco, a sessão está utilizável. Carregamos sessões com
// qualquer status exceto 'banned' e marcamos como 'active' ao subir.
//
// Isso resolve perda de conexão Cloud entre deploys: mesmo que o
// watchdog antigo tenha marcado como 'disconnected' por engano, ao
// reiniciar o app a sessão volta automaticamente.
func (cm *CloudManager) LoadAll(ctx context.Context) error {
	sessions, err := cm.repo.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.Type != db.SessionTypeCloudAPI {
			continue
		}
		// Mesmo que estiver marcada como 'disconnected' no banco, registra em memória
		// para que o webhook da Meta encontre a sessão e o envio outbound funcione.
		cm.register(&s)
		// Reativa no banco para que liste corretamente nas UIs.
		if s.Status != db.SessionActive {
			if err := cm.repo.UpdateSessionStatus(ctx, s.JID, db.SessionActive); err != nil {
				cm.log.Warn("cloud LoadAll: failed to reactivate session in db",
					zap.String("jid", s.JID), zap.Error(err))
			}
		}
	}
	cm.log.Info("cloud sessions loaded", zap.Int("count", len(cm.sessions)))
	return nil
}

func (cm *CloudManager) register(s *db.WhatsAppSession) {
	cs := &CloudSession{
		JID:           s.JID,
		PhoneNumberID: s.CloudPhoneNumberID,
		WABAID:        s.CloudWABAID,
		AccessToken:   s.CloudAccessToken,
		AppSecret:     s.CloudAppSecret,
		VerifyToken:   s.CloudVerifyToken,
		DisplayPhone:  s.CloudDisplayPhone,
		APIVersion:    cm.apiVer,
		BaseURL:       cm.baseURL,
	}
	cm.mu.Lock()
	cm.sessions[s.JID] = cs
	cm.mu.Unlock()
}

// AddSession registra uma nova sessão Cloud API e persiste no banco.
// Retorna o JID sintético gerado.
func (cm *CloudManager) AddSession(ctx context.Context, params CloudSessionParams) (*db.WhatsAppSession, error) {
	if params.PhoneNumberID == "" || params.AccessToken == "" {
		return nil, fmt.Errorf("phone_number_id e access_token são obrigatórios")
	}
	jid := CloudJIDFromPhoneID(params.PhoneNumberID)

	// Valida o token chamando GET /{phone_number_id} — confirma credencial.
	if err := cm.testCredentials(ctx, params); err != nil {
		return nil, fmt.Errorf("credenciais inválidas: %w", err)
	}

	s := &db.WhatsAppSession{
		ID:                 uuid.New(),
		JID:                jid,
		Phone:              params.DisplayPhone,
		DisplayName:        params.DisplayName(),
		Status:             db.SessionActive,
		SessionFile:        "",
		Type:               db.SessionTypeCloudAPI,
		CloudPhoneNumberID: params.PhoneNumberID,
		CloudWABAID:        params.WABAID,
		CloudAccessToken:   params.AccessToken,
		CloudVerifyToken:   params.VerifyToken,
		CloudAppSecret:     params.AppSecret,
		CloudDisplayPhone:  params.DisplayPhone,
	}
	if err := cm.repo.UpsertSession(ctx, s); err != nil {
		return nil, fmt.Errorf("upsert session: %w", err)
	}
	cm.register(s)
	cm.log.Info("cloud session added",
		zap.String("jid", jid),
		zap.String("phone", params.DisplayPhone),
		zap.String("phone_number_id", params.PhoneNumberID),
	)
	return s, nil
}

// RemoveSession desconecta a sessão Cloud API.
func (cm *CloudManager) RemoveSession(ctx context.Context, jid string) error {
	cm.mu.Lock()
	delete(cm.sessions, jid)
	cm.mu.Unlock()
	return cm.repo.UpdateSessionStatus(ctx, jid, db.SessionDisconnected)
}

// Get retorna a sessão pelo JID (case sensitive).
func (cm *CloudManager) Get(jid string) (*CloudSession, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	s, ok := cm.sessions[jid]
	return s, ok
}

// GetByPhoneID localiza a sessão pelo phone_number_id (usado no webhook).
func (cm *CloudManager) GetByPhoneID(phoneID string) (*CloudSession, bool) {
	jid := CloudJIDFromPhoneID(phoneID)
	return cm.Get(jid)
}

// ListJIDs retorna os JIDs sintéticos das sessões Cloud API ativas.
func (cm *CloudManager) ListJIDs() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	out := make([]string, 0, len(cm.sessions))
	for jid := range cm.sessions {
		out = append(out, jid)
	}
	return out
}

// CloudSessionParams agrupa as credenciais que o operador informa ao cadastrar.
type CloudSessionParams struct {
	PhoneNumberID string
	WABAID        string
	AccessToken   string
	AppSecret     string
	VerifyToken   string
	DisplayPhone  string // ex: "5511999999999"
	DisplayLabel  string // ex: "Suporte UCT (Oficial)" — opcional
}

// DisplayName retorna um nome legível: usa DisplayLabel se houver, senão "Cloud +<phone>".
func (p CloudSessionParams) DisplayName() string {
	if p.DisplayLabel != "" {
		return p.DisplayLabel
	}
	if p.DisplayPhone != "" {
		return "Cloud +" + p.DisplayPhone
	}
	return "Cloud " + p.PhoneNumberID
}

// PingSession verifica se a sessão Cloud está saudável fazendo uma chamada
// leve no Graph API (GET /{phone_number_id}). Retorna ok + mensagem de erro
// quando falha.
func (cm *CloudManager) PingSession(ctx context.Context, sessionJID string) (bool, string) {
	s, ok := cm.Get(sessionJID)
	if !ok {
		return false, "sessão Cloud não está em memória"
	}
	url := fmt.Sprintf("%s/%s/%s", s.BaseURL, s.APIVersion, s.PhoneNumberID)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	resp, err := cm.http.Do(req)
	if err != nil {
		return false, "falha na requisição: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return false, fmt.Sprintf("status %d: %s", resp.StatusCode, string(body))
	}
	s.mu.Lock()
	s.LastOK = time.Now()
	s.mu.Unlock()
	return true, ""
}

// testCredentials valida o token chamando GET /{phone_number_id}.
// Resposta esperada: {"verified_name": "...", "display_phone_number": "...", ...}
func (cm *CloudManager) testCredentials(ctx context.Context, p CloudSessionParams) error {
	url := fmt.Sprintf("%s/%s/%s", cm.baseURL, cm.apiVer, p.PhoneNumberID)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+p.AccessToken)
	resp, err := cm.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ─── Envio de mensagens ────────────────────────────────────────────────────

// SendText envia uma mensagem de texto. Retorna o wamid da Meta (usado como wa_message_id).
func (cm *CloudManager) SendText(ctx context.Context, sessionJID, toPhone, text string) (string, error) {
	s, ok := cm.Get(sessionJID)
	if !ok {
		return "", fmt.Errorf("cloud session not found: %s", sessionJID)
	}
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                normalizeRecipient(toPhone),
		"type":              "text",
		"text": map[string]interface{}{
			"preview_url": false,
			"body":        text,
		},
	}
	return cm.postMessage(ctx, s, payload)
}

// SendDocument envia um arquivo (imagem/document/audio/video).
// mediaType: "image" | "document" | "audio" | "video".
func (cm *CloudManager) SendDocument(ctx context.Context, sessionJID, toPhone string, data []byte, mime, fileName, mediaType string) (string, error) {
	s, ok := cm.Get(sessionJID)
	if !ok {
		return "", fmt.Errorf("cloud session not found: %s", sessionJID)
	}
	if mediaType == "" {
		mediaType = inferMediaType(mime)
	}
	mediaID, err := cm.uploadMedia(ctx, s, data, mime, fileName)
	if err != nil {
		return "", fmt.Errorf("upload media: %w", err)
	}
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                normalizeRecipient(toPhone),
		"type":              mediaType,
		mediaType: map[string]interface{}{
			"id": mediaID,
		},
	}
	if mediaType == "document" && fileName != "" {
		payload[mediaType].(map[string]interface{})["filename"] = fileName
	}
	return cm.postMessage(ctx, s, payload)
}

// SendDocumentByLink envia arquivo via URL pública. Diferente de SendDocument
// (que faz upload + media_id), aqui passamos só o link — a Meta baixa do nosso
// servidor e entrega ao cliente.
//
// Vantagem: a validação de MIME é mais permissiva nesse caminho. Tipos como
// ZIP, RAR, TAR que falham no upload direto podem passar via link.
//
// IMPORTANTE: a URL precisa ser PÚBLICA e retornar binary content (não HTML).
func (cm *CloudManager) SendDocumentByLink(ctx context.Context, sessionJID, toPhone, link, fileName, caption string) (string, error) {
	s, ok := cm.Get(sessionJID)
	if !ok {
		return "", fmt.Errorf("cloud session not found: %s", sessionJID)
	}
	doc := map[string]interface{}{
		"link": link,
	}
	if fileName != "" {
		doc["filename"] = fileName
	}
	if caption != "" {
		doc["caption"] = caption
	}
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                normalizeRecipient(toPhone),
		"type":              "document",
		"document":          doc,
	}
	return cm.postMessage(ctx, s, payload)
}

func (cm *CloudManager) uploadMedia(ctx context.Context, s *CloudSession, data []byte, mime, fileName string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/media", s.BaseURL, s.APIVersion, s.PhoneNumberID)
	if fileName == "" {
		fileName = "file"
	}
	if mime == "" {
		mime = "application/octet-stream"
	}

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("messaging_product", "whatsapp")
	_ = w.WriteField("type", mime)

	// CRÍTICO: a Meta valida o Content-Type DA PARTE "file" no multipart
	// (não o campo "type" separado). w.CreateFormFile sempre seta
	// "application/octet-stream" como default, então usamos CreatePart
	// manualmente com o Content-Type correto.
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename=%q`, escapeQuotes(fileName)))
	h.Set("Content-Type", mime)
	fw, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	_ = w.Close()

	req, _ := http.NewRequestWithContext(ctx, "POST", url, body)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := cm.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("media upload status %d: %s", resp.StatusCode, string(rb))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("parse media response: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("media id vazio na resposta: %s", string(rb))
	}
	return out.ID, nil
}

func (cm *CloudManager) postMessage(ctx context.Context, s *CloudSession, payload map[string]interface{}) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/messages", s.BaseURL, s.APIVersion, s.PhoneNumberID)
	jb, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jb))
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cm.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("send message status %d: %s", resp.StatusCode, string(rb))
	}
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", fmt.Errorf("parse send response: %w", err)
	}
	if len(out.Messages) == 0 || out.Messages[0].ID == "" {
		return "", fmt.Errorf("wamid ausente na resposta: %s", string(rb))
	}
	s.mu.Lock()
	s.LastOK = time.Now()
	s.mu.Unlock()
	return out.Messages[0].ID, nil
}

// ─── Download de mídia (usado pelo webhook ao receber inbound) ─────────────

// DownloadMedia baixa o conteúdo de uma mídia recebida via webhook.
// Retorna bytes + mime type efetivo.
func (cm *CloudManager) DownloadMedia(ctx context.Context, sessionJID, mediaID string) ([]byte, string, error) {
	s, ok := cm.Get(sessionJID)
	if !ok {
		return nil, "", fmt.Errorf("cloud session not found: %s", sessionJID)
	}
	// 1) GET /{media-id} → retorna {url, mime_type, ...}
	infoURL := fmt.Sprintf("%s/%s/%s", s.BaseURL, s.APIVersion, mediaID)
	req, _ := http.NewRequestWithContext(ctx, "GET", infoURL, nil)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	resp, err := cm.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("media info status %d: %s", resp.StatusCode, string(body))
	}
	var info struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, "", err
	}
	if info.URL == "" {
		return nil, "", fmt.Errorf("media URL vazia: %s", string(body))
	}
	// Bloqueia download se exceder 100MB — caller trata ErrMediaTooLarge
	// avisando o cliente e ignorando no Bitrix.
	if info.FileSize > MaxInboundMediaBytes {
		return nil, "", fmt.Errorf("%w: %d bytes", ErrMediaTooLarge, info.FileSize)
	}
	// 2) GET na URL com Bearer → bytes
	req2, _ := http.NewRequestWithContext(ctx, "GET", info.URL, nil)
	req2.Header.Set("Authorization", "Bearer "+s.AccessToken)
	resp2, err := cm.http.Do(req2)
	if err != nil {
		return nil, "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		return nil, "", fmt.Errorf("media download status %d", resp2.StatusCode)
	}
	data, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil, "", err
	}
	return data, info.MimeType, nil
}

// ─── Helpers ───────────────────────────────────────────────────────────────

// normalizeRecipient remove qualquer "@s.whatsapp.net" / "@lid" / "+" / espaços
// — Cloud API espera apenas o número E.164 sem prefixo.
func normalizeRecipient(toPhone string) string {
	if at := strings.Index(toPhone, "@"); at != -1 {
		toPhone = toPhone[:at]
	}
	if colon := strings.Index(toPhone, ":"); colon != -1 {
		toPhone = toPhone[:colon]
	}
	out := make([]byte, 0, len(toPhone))
	for _, ch := range toPhone {
		if ch >= '0' && ch <= '9' {
			out = append(out, byte(ch))
		}
	}
	return string(out)
}

// inferMediaType escolhe "image" | "audio" | "video" | "document" pelo MIME.
func inferMediaType(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	default:
		return "document"
	}
}
