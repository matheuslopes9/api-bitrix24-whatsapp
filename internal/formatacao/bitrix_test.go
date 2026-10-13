package formatacao

import "testing"

func TestParaBitrix(t *testing.T) {
	casos := []struct{ nome, entra, sai string }{
		{"sem formatacao passa intacto", "ola, tudo bem?", "ola, tudo bem?"},
		{"print do chamado", "teste\n\n*formatação*\n\n_de_\n\n~layout~",
			"teste\n\n[B]formatação[/B]\n\n[I]de[/I]\n\n[S]layout[/S]"},
		{"aninhado", "*negrito com _italico_ dentro*", "[B]negrito com [I]italico[/I] dentro[/B]"},
		{"rotulo do espelho", "📤 *Mensagem enviada externamente (Ester Santos):*\nteste",
			"📤 [B]Mensagem enviada externamente (Ester Santos):[/B]\nteste"},
		{"remetente de grupo", "*Fulano:* bom dia", "[B]Fulano:[/B] bom dia"},
		{"sem borda nao formata", "2*3*4 e snake_case_var", "2*3*4 e snake_case_var"},
		{"nao atravessa linha", "*multi\nlinha*", "*multi\nlinha*"},
		{"bloco de codigo", "```\nlinha 1\n*nao*\n```", "[CODE]linha 1\n*nao*[/CODE]"},
		{"crase sozinha na linha vira bloco", "Seu pedido:\n`PED-4471`\nobrigado",
			"Seu pedido:\n[CODE]PED-4471[/CODE]\nobrigado"},
		{"crase sozinha na mensagem", "`PED-4471`", "[CODE]PED-4471[/CODE]"},
		{"crase no meio da frase fica", "use o `cupom10` hoje", "use o `cupom10` hoje"},
		{"crase dentro de negrito fica", "*`x`*", "[B]`x`[/B]"},
		{"lista com asterisco vira bolinha", "* um\n* *dois*", "• um\n• [B]dois[/B]"},
		{"lista com hifen e numerada ficam", "- um\n1. _dois_", "- um\n1. [I]dois[/I]"},
		{"citacao", "> citado *forte*\n> linha 2\nfora", ">>citado [B]forte[/B]\n>>linha 2\nfora"},
		{"citacao sem espaco nao e' citacao", ">x", ">x"},
		{"cliente nao injeta botao", "[SEND=oi]clique[/SEND]", "[​SEND=oi]clique[​/SEND]"},
		{"cliente nao injeta link", "[URL=https://x.io]banco[/url]", "[​URL=https://x.io]banco[​/url]"},
		{"cliente nao fecha o bloco", "```a [/CODE] b```", "[CODE]a [​/CODE] b[/CODE]"},
		// Qualquer [palavra] leva o U+200B: cobre codigos que o Bitrix ainda
		// vai criar, e o caractere nao aparece.
		{"colchete com palavra leva o U+200B", "[audio] e [1] e a[b", "[​audio] e [1] e a[b"},
	}
	for _, c := range casos {
		if got := ParaBitrix(c.entra); got != c.sai {
			t.Errorf("%s\n  entrada: %q\n  quer:    %q\n  veio:    %q", c.nome, c.entra, c.sai, got)
		}
	}
}
