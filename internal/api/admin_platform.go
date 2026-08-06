// admin_platform.go — recursos avancados do painel admin:
//   - Gerenciador de usuarios admin (multi-admin, bcrypt, papeis)
//   - Log de auditoria
//   - Monitoramento do processo (CPU/RAM/goroutines/DB/Redis/filas)
//   - Consumo por tenant
//   - Gestao de IPs bloqueados
package api

import (
	"bufio"
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/logbuffer"
	"github.com/valyala/fasthttp"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// processStartTime marca o boot pra calcular uptime.
var processStartTime = timeNowSafe()

// timeNowSafe existe so' pra permitir init sem depender de time.Now no
// escopo global de teste. Em runtime normal e' time.Now().
func timeNowSafe() time.Time { return time.Now() }

// ─── Gerenciador de usuarios admin ─────────────────────────────────────────

// adminActor extrai um rotulo do admin logado pra auditoria. Hoje o login
// e' unico (env), entao usamos o AdminUser do env; quando multi-admin login
// estiver 100%, sai do cookie. Best-effort.
func (h *handlers) adminActor(c *fiber.Ctx) string {
	if a, ok := c.Locals("admin_actor").(string); ok && a != "" {
		return a
	}
	return h.cfg.App.AdminUser
}

// tryDBAdminLogin valida credenciais contra os admins do banco (bcrypt).
// Retorna true se um usuario ATIVO bateu email+senha. Atualiza last_login.
func (h *handlers) tryDBAdminLogin(c *fiber.Ctx, email, password string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return false
	}
	u, err := h.repo.GetAdminUserByEmail(c.Context(), email)
	if err != nil || u == nil || !u.Active {
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return false
	}
	_ = h.repo.TouchAdminUserLogin(c.Context(), u.ID)
	return true
}

// GET /admin/api/users — lista admins do banco (sem hash).
func (h *handlers) adminListUsers(c *fiber.Ctx) error {
	users, err := h.repo.ListAdminUsers(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"users": users, "root_user": h.cfg.App.AdminUser})
}

// POST /admin/api/users — cria admin. Body {email, name, password, role}.
func (h *handlers) adminCreateUser(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Role = strings.ToLower(strings.TrimSpace(req.Role))
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return c.Status(400).JSON(fiber.Map{"error": "email invalido"})
	}
	if len(req.Password) < 8 {
		return c.Status(400).JSON(fiber.Map{"error": "senha precisa de ao menos 8 caracteres"})
	}
	if req.Role != "superadmin" && req.Role != "support" {
		req.Role = "support"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "falha ao gerar hash"})
	}
	u := &db.AdminUser{
		ID: uuid.New(), Email: req.Email, Name: strings.TrimSpace(req.Name),
		PasswordHash: string(hash), Role: req.Role, Active: true,
		CreatedBy: h.adminActor(c),
	}
	if err := h.repo.CreateAdminUser(c.Context(), u); err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return c.Status(409).JSON(fiber.Map{"error": "ja existe um admin com esse email"})
		}
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "user.create", req.Email, "role="+req.Role, clientIP(c))
	return c.JSON(fiber.Map{"ok": true, "id": u.ID.String()})
}

// POST /admin/api/users/toggle — ativa/desativa. Body {id, active}.
func (h *handlers) adminToggleUser(c *fiber.Ctx) error {
	var req struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "id invalido"})
	}
	if err := h.repo.SetAdminUserActive(c.Context(), id, req.Active); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "user.toggle", req.ID, "active="+boolStr(req.Active), clientIP(c))
	return c.JSON(fiber.Map{"ok": true})
}

// POST /admin/api/users/delete — remove admin. Body {id}.
func (h *handlers) adminDeleteUser(c *fiber.Ctx) error {
	var req struct {
		ID string `json:"id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	id, err := uuid.Parse(req.ID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "id invalido"})
	}
	if err := h.repo.DeleteAdminUser(c.Context(), id); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "user.delete", req.ID, "", clientIP(c))
	return c.JSON(fiber.Map{"ok": true})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// ─── Auditoria ─────────────────────────────────────────────────────────────

// GET /admin/api/audit — ultimas N entradas.
func (h *handlers) adminAuditLog(c *fiber.Ctx) error {
	entries, err := h.repo.ListAudit(c.Context(), 300)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"entries": entries})
}

// ─── Monitoramento do processo ─────────────────────────────────────────────

// GET /admin/api/system — metricas do processo/container em tempo real.
func (h *handlers) adminSystem(c *fiber.Ctx) error {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	out := fiber.Map{
		"go_version":     runtime.Version(),
		"num_cpu":        runtime.NumCPU(),
		"num_goroutine":  runtime.NumGoroutine(),
		"heap_alloc_mb":  float64(mem.HeapAlloc) / 1024 / 1024,
		"heap_sys_mb":    float64(mem.HeapSys) / 1024 / 1024,
		"stack_sys_mb":   float64(mem.StackSys) / 1024 / 1024,
		"total_alloc_mb": float64(mem.TotalAlloc) / 1024 / 1024,
		"num_gc":         mem.NumGC,
		"uptime_seconds": int64(time.Since(processStartTime).Seconds()),
	}

	// Postgres pool stats.
	if stat := h.repo.Pool().Stat(); stat != nil {
		out["db_conns_total"] = stat.TotalConns()
		out["db_conns_idle"] = stat.IdleConns()
		out["db_conns_used"] = stat.AcquiredConns()
		out["db_conns_max"] = stat.MaxConns()
	}

	// Redis ping (latencia).
	ctx, cancel := context.WithTimeout(c.Context(), 2*time.Second)
	defer cancel()
	redisOK := false
	redisMs := int64(-1)
	if h.q != nil {
		t0 := time.Now()
		if err := h.q.Ping(ctx); err == nil {
			redisOK = true
			redisMs = time.Since(t0).Milliseconds()
		}
	}
	out["redis_ok"] = redisOK
	out["redis_ping_ms"] = redisMs

	// Filas.
	if h.q != nil {
		inb, outb, dead := h.q.Lengths(ctx)
		out["queue_inbound"] = inb
		out["queue_outbound"] = outb
		out["queue_dead"] = dead
	}

	// Sessoes WA ativas em memoria.
	if h.waManager != nil {
		out["wa_sessions_live"] = len(h.waManager.ListSessions())
	}

	return c.JSON(out)
}

// ─── Logs em tempo real (SSE) ──────────────────────────────────────────────

// GET /admin/api/logs/stream — Server-Sent Events com as ultimas linhas do
// ring buffer + novas em tempo real. O front consome via EventSource.
func (h *handlers) adminLogsStream(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no") // evita buffering no proxy

	ch, cancel := logbuffer.Subscribe()
	snapshot := logbuffer.Snapshot()

	c.Context().SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
		defer cancel()
		// Envia o historico recente primeiro (ultimas ~80 linhas).
		start := 0
		if len(snapshot) > 80 {
			start = len(snapshot) - 80
		}
		for _, ln := range snapshot[start:] {
			fmt.Fprintf(w, "data: %s\n\n", ln.Text)
		}
		if err := w.Flush(); err != nil {
			return
		}
		// Stream ao vivo + heartbeat pra manter a conexao.
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case ln, ok := <-ch:
				if !ok {
					return
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", ln.Text); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			case <-ticker.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	}))
	return nil
}

// ─── Consumo por tenant ────────────────────────────────────────────────────

// GET /admin/api/usage — consumo agregado por tenant.
func (h *handlers) adminUsage(c *fiber.Ctx) error {
	usage, err := h.repo.GetTenantUsage(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"usage": usage})
}

// ─── IPs bloqueados ────────────────────────────────────────────────────────

// GET /admin/api/blocked-ips — lista.
func (h *handlers) adminListBlockedIPs(c *fiber.Ctx) error {
	ips, err := h.repo.ListBlockedIPs(c.Context())
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"blocked": ips})
}

// POST /admin/api/blocked-ips/block — bloqueia manualmente. Body {ip, note}.
func (h *handlers) adminBlockIP(c *fiber.Ctx) error {
	var req struct {
		IP   string `json:"ip"`
		Note string `json:"note"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.IP = strings.TrimSpace(req.IP)
	if req.IP == "" {
		return c.Status(400).JSON(fiber.Map{"error": "ip obrigatorio"})
	}
	if err := h.repo.UpsertBlockedIP(c.Context(), req.IP, "manual", strings.TrimSpace(req.Note), 0); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "ip.block", req.IP, req.Note, clientIP(c))
	return c.JSON(fiber.Map{"ok": true})
}

// POST /admin/api/blocked-ips/unblock — libera. Body {ip}.
func (h *handlers) adminUnblockIP(c *fiber.Ctx) error {
	var req struct {
		IP string `json:"ip"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "JSON invalido"})
	}
	req.IP = strings.TrimSpace(req.IP)
	if req.IP == "" {
		return c.Status(400).JSON(fiber.Map{"error": "ip obrigatorio"})
	}
	if err := h.repo.SetBlockedIPActive(c.Context(), req.IP, false); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	// Tambem limpa o rate-limit em memoria pra liberar na hora.
	loginRecordSuccess(req.IP)
	h.repo.WriteAudit(c.Context(), h.adminActor(c), "ip.unblock", req.IP, "", clientIP(c))
	h.log.Info("admin: ip desbloqueado", zap.String("ip", req.IP))
	return c.JSON(fiber.Map{"ok": true})
}
