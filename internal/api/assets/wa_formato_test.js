// Confere o waFormato contra o parser do proprio WhatsApp Web — o mesmo
// gabarito do teste Go (internal/formatacao/testdata/whatsapp_web.json).
//
//   node internal/api/assets/wa_formato_test.js
'use strict';
var fs = require('fs');
var path = require('path');
var wa = require('./wa_formato.js');

var casos = JSON.parse(fs.readFileSync(path.join(__dirname, '../../formatacao/testdata/whatsapp_web.json'), 'utf8'));

function notacao(nos) {
  return nos.map(function (n) {
    if (n.texto != null) return n.texto.replace(/\n/g, '⏎');
    if (n.delim != null) return '';
    var tag = { b: 'b', i: 'i', s: 's', code: 'code', icode: 'icode', ul: 'ul', ol: 'ol', lin: 'li#', q: 'q' }[n.tipo];
    if (n.tipo === 'li') tag = n.marcador === '-' ? 'li-' : 'li*';
    return '<' + tag + '>' + notacao(n.filhos) + '</' + tag + '>';
  }).join('');
}

var falhas = 0;
casos.forEach(function (c) {
  var got = notacao(wa.parse(c[0]).filhos);
  if (got !== c[1]) {
    if (++falhas <= 15) console.log('entrada ' + JSON.stringify(c[0]) + '\n  whatsapp: ' + c[1] + '\n  nosso:    ' + got);
  }
});

// O HTML nao pode deixar passar marcacao do cliente.
var html = wa.html('<img src=x onerror=alert(1)> *<b>x</b>*');
if (html.indexOf('<img') >= 0 || html.indexOf('<b>x') >= 0) { console.log('HTML sem escape: ' + html); falhas++; }

if (falhas) { console.log(falhas + ' de ' + casos.length + ' casos divergem do WhatsApp Web'); process.exit(1); }
console.log('ok: ' + casos.length + ' casos iguais ao WhatsApp Web');
