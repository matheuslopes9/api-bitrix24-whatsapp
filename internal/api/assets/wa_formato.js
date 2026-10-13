// waFormato — le a formatacao do WhatsApp (*negrito*, _italico_, ~riscado~,
// codigo entre crases, bloco entre tres crases, listas e citacao) e desenha
// em HTML na aba UC Talk.
//
// E' o mesmo motor de internal/formatacao/whatsapp.go, que por sua vez e' um
// porte do parser do WhatsApp Web (WAWebFormatParse/Match/Filter/ParseTree).
// Aqui as regex sao as dele quase literais; a classe de borda gigante dele e'
// exatamente \p{P}\p{S}\p{Z}\p{Cf} + \t-\r (conferido codepoint a codepoint).
// O teste roda com node contra internal/formatacao/testdata/whatsapp_web.json:
//
//   node internal/api/assets/wa_formato_test.js
//
// Este arquivo vai embutido no HTML da aba (crm.go), que e' uma raw string do
// Go: nao pode haver crase literal em lugar nenhum. Nas regex ela e' \x60.
(function (raiz) {
  'use strict';

  var B = '[\\p{P}\\p{S}\\p{Z}\\p{Cf}\\t-\\r]';
  var ANTES = '(?:^|' + B + ')';
  var DEPOIS = '(?=$|' + B + ')';
  var CONTEUDO = '([^\\s]|[^\\s][^\\n]*?[^\\s])';

  function delimitado(d, abre) {
    return new RegExp(ANTES + d + abre + CONTEUDO + d + DEPOIS, 'gu');
  }

  var M = {
    codigo: { tipo: 'code', grupo: 0, formata: false, re: /\x60\x60\x60([\s\S]*?\S[\s\S]*?)\x60\x60\x60/g, g: 1 },
    icode: { tipo: 'icode', grupo: 1, formata: false, re: delimitado('\\x60', '(?![\\s\\x60])'), g: 1, d: 1 },
    li: { tipo: 'li', grupo: 2, re: /^[\*•]( (?! +)[^\n]+$)/gm, g: 1, so: 'ul' },
    lih: { tipo: 'li', grupo: 3, re: /^\-( (?! +)[^\n]+$)/gm, g: 1, so: 'ul' },
    ul: { tipo: 'ul', grupo: 4, re: /((?:^)(?:[\*\-•] (?! +)(?:[^\n]+)(?:\n(?:[\*\-•] (?! +)[^\n]+)){0,})(?:\n|$))/gm, g: 0, nunca: true },
    lin: { tipo: 'lin', grupo: 5, re: /^(\d{1,2}\.)( (?! +)[^\n]+$)/gm, g: 2, so: 'ol' },
    ol: { tipo: 'ol', grupo: 6, re: /((?:^)(?:(\d{1,2})\. (?! +)(?:[^\n]+)(?:\n(?:(\d{1,2})\. [^\n]+)){0,})(?:\n|$))/gm, g: 0, nunca: true },
    b: { tipo: 'b', grupo: 7, re: new RegExp(ANTES + '\\*(?![\\s]|\\u20E3|\\uFE0F\\u20E3)([^\\n]*?[^\\s])\\*(?=$|(?!\\u20E3|\\uFE0F\\u20E3))' + DEPOIS, 'gu'), g: 1, d: 1 },
    i: { tipo: 'i', grupo: 7, re: delimitado('_', ''), g: 1, d: 1 },
    s: { tipo: 's', grupo: 7, re: delimitado('~', ''), g: 1, d: 1 },
    q: { tipo: 'q', grupo: 8, re: /^>([^\S\n](?! +)[^\n]*$(?:\n|$))/gm, g: 1 }
  };
  var ORDEM = [M.codigo, M.icode, M.li, M.lih, M.ul, M.lin, M.ol, M.b, M.i, M.s, M.q];
  ORDEM.forEach(function (m) { if (m.formata === undefined) m.formata = true; });

  function aninha(m, pai) {
    if (m.nunca) return false;
    if (m.so) return pai.tipo === m.so;
    return true;
  }

  // WAWebExtractRangesUsingRegex + WAWebAdjustDelimitedRange +
  // WAWebExtractRangeFromMatch
  function extrair(texto, m, idx) {
    var out = [], r;
    m.re.lastIndex = 0;
    while ((r = m.re.exec(texto)) && m.re.lastIndex > 0) {
      var ini = r.index, inteiro = r[0];
      if (m.d) {
        var sobra = inteiro.length - (2 * m.d + r[1].length);
        if (sobra > 0) { ini += sobra; inteiro = inteiro.slice(sobra); }
        m.re.lastIndex--;
      }
      var fim = ini + inteiro.length - 1, f = [ini, ini, fim, fim, idx, r];
      if (m.g) {
        var g = r[m.g] == null ? '' : r[m.g];
        f[1] = ini + inteiro.indexOf(g);
        f[2] = f[1] + g.length - 1;
      }
      out.push(f);
    }
    return out;
  }

  // WAWebFormatMatch: todos os matches, do inicio maior para o menor.
  function casar(texto) {
    var r = [];
    for (var o = 0; o < ORDEM.length; o++) {
      var u = extrair(texto, ORDEM[o], o);
      if (u.length) r.push(u);
    }
    var y = r.map(function (l) { return l.length - 1; }), b = [];
    while (r.length) {
      var v = 0, maior = -1;
      for (var s = 0; s < r.length; s++) {
        if (r[s][y[s]][0] > maior) { v = s; maior = r[s][y[s]][0]; }
      }
      b.push(r[v][y[v]]);
      if (--y[v] < 0) { r.splice(v, 1); y.splice(v, 1); }
    }
    return b;
  }

  // WAWebFormatFilter
  function filtrar(e) {
    if (e.length === 1) return e;
    var n = [], r;
    while ((r = e.pop())) {
      var u = ORDEM[r[4]], ok = true;
      for (var p = n.length - 1; p >= 0; --p) {
        var ac = n[p];
        if (r[0] > ac[3]) continue;
        var c = ORDEM[ac[4]];
        if (r[3] > ac[2]) {
          if (u.grupo < c.grupo) { n.splice(p, 1); continue; }
          ok = false; break;
        }
        if (!aninha(u, c) && u.grupo < c.grupo) { n.splice(p, 1); continue; }
        if (!c.formata) { ok = false; break; }
      }
      if (ok) n.push(r);
    }
    return n;
  }

  // WAWebFormatParseTree
  function arvore(texto, fs) {
    var raiz = { filhos: [] }, i = raiz, a = 0, l;
    function fechar(no) {
      var L = no.f[2], E = no.f[3];
      if (a <= L) no.filhos.push({ texto: texto.substring(a, L + 1) });
      a = E + 1;
      if (L !== E) no.pai.filhos.push({ delim: texto.substring(L + 1, a) });
    }
    fs.forEach(function (c) {
      var d = c[0], m = c[1];
      while (i.pai && d > (l = i.f[2])) { fechar(i); i = i.pai; }
      if (a < d) i.filhos.push({ texto: texto.substring(a, d) });
      a = m;
      var marcador = d !== m ? texto.substring(d, m) : '';
      if (marcador) i.filhos.push({ delim: marcador });
      var no = { tipo: ORDEM[c[4]].tipo, marcador: marcador, filhos: [], f: c, pai: i };
      i.filhos.push(no);
      i = no;
    });
    while (i.pai) { fechar(i); i = i.pai; }
    if (a !== texto.length) raiz.filhos.push({ texto: texto.substring(a) });
    return raiz;
  }

  function parse(texto) {
    texto = String(texto == null ? '' : texto);
    var fs = casar(texto);
    if (!fs.length) return { filhos: [{ texto: texto }] };
    return arvore(texto, filtrar(fs));
  }

  function esc(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/\n/g, '<br>');
  }

  function puro(nos) {
    return nos.map(function (n) { return n.texto != null ? n.texto : puro(n.filhos || []); }).join('');
  }

  function html(nos) {
    var out = '';
    nos.forEach(function (n) {
      if (n.texto != null) { out += esc(n.texto); return; }
      if (n.delim != null) return;
      switch (n.tipo) {
        case 'b': out += '<b>' + html(n.filhos) + '</b>'; break;
        case 'i': out += '<i>' + html(n.filhos) + '</i>'; break;
        case 's': out += '<s>' + html(n.filhos) + '</s>'; break;
        case 'code': out += '<span class="wa-code">' + esc(puro(n.filhos).replace(/^\n+|\n+$/g, '')) + '</span>'; break;
        case 'icode': out += '<code class="wa-icode">' + esc(puro(n.filhos)) + '</code>'; break;
        case 'li': out += '<span class="wa-li">' + (n.marcador === '-' ? '-' : '•') + '</span>' + html(n.filhos); break;
        case 'lin': out += '<span class="wa-li">' + esc(n.marcador) + '</span>' + html(n.filhos); break;
        case 'q':
          var f = n.filhos.slice();
          if (f.length && f[0].texto != null) f[0] = { texto: f[0].texto.replace(/^[ \t]/, '') };
          var ult = f.length - 1;
          if (ult >= 0 && f[ult].texto != null) f[ult] = { texto: f[ult].texto.replace(/\n$/, '') };
          out += '<span class="wa-q">' + html(f) + '</span>';
          break;
        default: out += html(n.filhos); // ul, ol
      }
    });
    return out;
  }

  var api = {
    parse: parse,
    html: function (texto) { return html(parse(texto).filhos); }
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else raiz.waFormato = api;
})(this);
