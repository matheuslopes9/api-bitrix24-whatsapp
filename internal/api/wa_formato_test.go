package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A aba UC Talk desenha *negrito*, _italico_ etc. com assets/wa_formato.js,
// que entra no HTML por um marcador (init em crm.go). Se o marcador sumir do
// HTML, a aba volta a mostrar os asteriscos crus sem erro nenhum — fmtMsg cai
// no esc() calado.
func TestAbaCRMLevaOFormatadorDoWhatsApp(t *testing.T) {
	if strings.Contains(crmTabHTML, "/*__WA_FORMATO__*/") {
		t.Fatal("marcador do formatador nao foi substituido no HTML da aba")
	}
	if !strings.Contains(crmTabHTML, "raiz.waFormato = api") {
		t.Fatal("o formatador do WhatsApp nao esta' no HTML da aba")
	}
	if strings.Count(crmTabHTML, "fmtMsg(") < 4 {
		t.Fatal("as bolhas da aba nao passam mais pelo fmtMsg")
	}
}

// O arquivo vai pra dentro de uma raw string do Go no runtime, e o
// markdown do WhatsApp usa crase: e' facil escrever uma no JS sem pensar.
// Hoje o Replace no init nao quebraria com ela, mas o arquivo promete nao ter
// nenhuma, e quem o mover pra dentro do crm.go confia nisso.
func TestFormatadorSemCraseLiteral(t *testing.T) {
	if strings.Contains(waFormatoJS, "`") {
		t.Fatal("assets/wa_formato.js tem crase literal; use \\x60")
	}
}

// Roda o teste do formatador JS contra o gabarito do WhatsApp Web, quando ha'
// node na maquina. O mesmo gabarito confere o porte Go em internal/formatacao.
func TestFormatadorJSIgualAoWhatsAppWeb(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node nao instalado; rode: node internal/api/assets/wa_formato_test.js")
	}
	script, _ := filepath.Abs(filepath.Join("assets", "wa_formato_test.js"))
	if _, err := os.Stat(script); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script).CombinedOutput()
	if err != nil {
		t.Fatalf("formatador JS diverge do WhatsApp Web:\n%s", out)
	}
}
