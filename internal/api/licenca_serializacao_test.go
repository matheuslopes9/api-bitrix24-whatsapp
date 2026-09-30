package api

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/uctechnology/api-bitrix24-whatsapp/internal/db"
)

// A tela de Licencas dizia "Sem licenca" para clientes com licenca em vigor, e
// a de Clientes mostrava "ate Invalid Date" na mesma licenca. Nenhuma das duas
// estava quebrada sozinha: elas discordavam.
//
// O mesmo JS (licBadge, fmtDia) renderiza os dois endpoints, mas cada um
// serializava a licenca do seu jeito — um mandava `configurada`, o outro
// `licenca_configurada`; um mandava a data como YYYY-MM-DD, o outro em
// RFC3339. Cada tela leu o campo que nao existia e degradou em silencio, que e'
// o pior desfecho possivel: a UI afirmava algo falso com toda a confianca.
//
// Estes testes travam o contrato dos dois lados. Renomear um campo agora
// quebra aqui, e nao na tela do suporte.

func TestLicenseSummaryUsaOsNomesQueAUILe(t *testing.T) {
	ate := time.Date(2027, 9, 24, 0, 0, 0, 0, time.UTC)
	out := licenseSummary(&db.TenantLicense{
		Domain: "teclife.bitrix24.com.br", MaxSessions: 5,
		FeatCloudAPI: true, ValidUntil: &ate,
	})

	for _, campo := range []string{"configurada", "max_sessions", "feat_cloud_api", "valid_until", "expirada"} {
		if _, ok := out[campo]; !ok {
			t.Errorf("licenseSummary nao emite %q — licBadge/featTags leem esse nome", campo)
		}
	}
	if v, _ := out["valid_until"].(string); v != "2027-09-24" {
		// fmtDia faz new Date(s+'T12:00:00'): qualquer outro formato vira
		// "Invalid Date" na tela, sem erro em lugar nenhum.
		t.Errorf("valid_until = %q, esperado YYYY-MM-DD — fmtDia depende disso", v)
	}
}

func TestLicenseSummarySemLicenca(t *testing.T) {
	out := licenseSummary(nil)
	if cfg, _ := out["configurada"].(bool); cfg {
		t.Error("licenca ausente marcada como configurada")
	}
	if _, temData := out["valid_until"]; temData {
		t.Error("licenca ausente nao pode ter vigencia — a tela mostraria um prazo inventado")
	}
}

// O painel admin nao pode mais citar o nome antigo: se citar, alguem ressuscitou
// a divergencia.
func TestPainelAdminNaoUsaNomeAntigoDaLicenca(t *testing.T) {
	for _, morto := range []string{"licenca_configurada", "licenca_notes"} {
		if strings.Contains(adminHomeHTML, morto) {
			t.Errorf("painel admin ainda le %q — o backend emite outro nome, o campo chega undefined", morto)
		}
	}
}

// Todo campo que o JS le de um objeto de licenca tem que existir na
// serializacao. Sem isso, o campo novo so' aparece como valor vazio na tela.
func TestCamposDeLicencaLidosPeloJSExistem(t *testing.T) {
	ate := time.Date(2027, 9, 24, 0, 0, 0, 0, time.UTC)
	out := licenseSummary(&db.TenantLicense{
		Domain: "x.bitrix24.com.br", MaxSessions: 5, ValidUntil: &ate,
	})

	// Recorta licBadge e le os t.<campo> que ela consulta.
	ini := strings.Index(adminHomeHTML, "function licBadge(")
	if ini < 0 {
		t.Fatal("licBadge sumiu do painel — este teste precisa ser reapontado")
	}
	corpo := adminHomeHTML[ini : ini+400]
	re := regexp.MustCompile(`\bt\.([a-z_]+)`)
	for _, m := range re.FindAllStringSubmatch(corpo, -1) {
		if _, ok := out[m[1]]; !ok {
			t.Errorf("licBadge le t.%s, que licenseSummary nao emite", m[1])
		}
	}
}
