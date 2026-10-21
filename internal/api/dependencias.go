// dependencias.go — a tela "Dependências" do painel admin e o aviso diario.
//
// O motor esta' em internal/dependencias; aqui ficam o ambiente (versoes que
// so' o processo em execucao sabe), as rotas e o job que manda e-mail quando
// algo fica critico. Ver internal/dependencias/dependencias.go pro PORQUE.
package api

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/dependencias"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/email"
	"github.com/uctechnology/api-bitrix24-whatsapp/internal/whatsapp"
	"go.uber.org/zap"
)

const (
	alertaDependencia = "dependencia"
	// Item critico continua critico ate' alguem atualizar — uma semana entre
	// lembretes e' o bastante pra nao virar ruido.
	janelaAlertaDependencia = 7 * 24 * time.Hour
	intervaloDependencias   = 24 * time.Hour
)

// ambienteDoProcesso responde as versoes de PostgreSQL, Redis e Alpine.
type ambienteDoProcesso struct{ h *handlers }

func (a ambienteDoProcesso) VersaoPostgres(ctx context.Context) string {
	return a.h.repo.VersaoPostgres(ctx)
}

func (a ambienteDoProcesso) VersaoRedis(ctx context.Context) string {
	if a.h.q == nil {
		return ""
	}
	return a.h.q.VersaoRedis(ctx)
}

// VersaoAlpine le o sistema do container. Fora dele (maquina de dev) o
// arquivo nao existe e a tela mostra "desconhecido".
func (a ambienteDoProcesso) VersaoAlpine() string {
	b, err := os.ReadFile("/etc/alpine-release")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (h *handlers) verificadorDependencias() *dependencias.Verificador {
	h.depsOnce.Do(func() {
		h.deps = dependencias.Novo(ambienteDoProcesso{h: h}, whatsapp.VersaoGraphAPI)
	})
	return h.deps
}

// IniciarDependencias verifica uma vez por dia e avisa o que ficou critico.
func (h *handlers) IniciarDependencias(ctx context.Context) {
	go func() {
		// Deixa o boot assentar (banco, Redis, sessoes) antes da primeira rodada.
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Minute):
		}
		for {
			semPanico(h.log, "verificacao de dependencias", func() { h.verificarDependencias(ctx) })
			select {
			case <-ctx.Done():
				return
			case <-time.After(intervaloDependencias):
			}
		}
	}()
}

func (h *handlers) verificarDependencias(pai context.Context) {
	ctx, cancel := context.WithTimeout(pai, 2*time.Minute)
	defer cancel()
	r := h.verificadorDependencias().Verificar(ctx)
	h.log.Info("dependencias verificadas",
		zap.Int("criticos", r.Criticos), zap.Int("atualizar", r.Atualizar), zap.Int("total", len(r.Itens)))
	if r.Criticos == 0 {
		return
	}
	cfg, err := h.repo.GetConfigAlertas(ctx)
	if err != nil || cfg == nil || !cfg.Configurado() {
		return // sem envio configurado: fica na tela e no log
	}
	rem := remetenteDaConfig(cfg)
	for _, it := range r.Itens {
		if it.Status != dependencias.Critico {
			continue
		}
		avisoID, ok, err := h.repo.DeveAvisar(ctx, alertaDependencia, it.Nome, "", janelaAlertaDependencia)
		if err != nil || !ok {
			continue
		}
		contexto := []email.LinhaContexto{
			{Rotulo: "Item", Valor: it.Nome},
			{Rotulo: "Para que serve", Valor: it.ParaQue},
			{Rotulo: "Em uso", Valor: naoVazio(it.EmUso, "desconhecida")},
		}
		if it.MaisNova != "" {
			contexto = append(contexto, email.LinhaContexto{Rotulo: "Mais nova", Valor: it.MaisNova})
		}
		if it.FimSuporte != nil {
			contexto = append(contexto, email.LinhaContexto{Rotulo: "Fim do suporte", Valor: it.FimSuporte.Format("02/01/2006")})
		}
		corpo := h.montarAlerta(email.CatDependencia,
			"Dependência precisa de atualização — "+it.Nome,
			"<p><b>"+it.Nome+"</b> ("+it.ParaQue+") está em situação crítica.</p>",
			it.Detalhe, contexto)
		if err := rem.Enviar(ctx, "[UC Talk] Atualizacao necessaria — "+it.Nome, corpo); err != nil {
			h.log.Error("alertas: falha ao enviar aviso de dependencia", zap.String("item", it.Nome), zap.Error(err))
			_ = h.repo.DesfazerAviso(ctx, avisoID)
			continue
		}
		h.log.Info("alerta enviado: dependencia critica", zap.String("item", it.Nome))
	}
}

// GET /admin/api/dependencias — o ultimo relatorio. Na primeira abertura
// depois de um deploy ainda nao ha' relatorio: verifica na hora.
func (h *handlers) adminDependencias(c *fiber.Ctx) error {
	v := h.verificadorDependencias()
	r := v.Ultimo()
	if r == nil && c.Query("so_cache") == "1" {
		// Contador do menu, que atualiza a cada minuto: nunca dispara consulta externa.
		return c.JSON(fiber.Map{"itens": []any{}, "criticos": 0, "atualizar": 0})
	}
	if r == nil {
		ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
		defer cancel()
		r = v.Verificar(ctx)
	}
	return c.JSON(r)
}

// POST /admin/api/dependencias/verificar — consulta tudo agora.
func (h *handlers) adminVerificarDependencias(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.Context(), 60*time.Second)
	defer cancel()
	return c.JSON(h.verificadorDependencias().Verificar(ctx))
}
