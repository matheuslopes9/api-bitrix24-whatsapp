// Aviso de acesso bloqueado nas telas do CLIENTE (painel, aba do CRM, menu
// do app). Vai embutido no <head> de cada uma (ver bloqueio.go).
//
// Quando o cliente e' bloqueado no painel admin, toda chamada da tela dele
// responde 403 com codigo "bloqueado". Sem isto, cada tela quebraria do seu
// jeito — lista vazia, spinner eterno, "erro ao carregar" — e o cliente nao
// saberia o que aconteceu nem com quem falar.
//
// Embutido em raw string do Go: nada de crase neste arquivo.
(function () {
  'use strict';
  if (!window.fetch) return;
  var original = window.fetch;
  var mostrado = false;

  function mostrar() {
    if (mostrado) return;
    mostrado = true;
    var ov = document.createElement('div');
    ov.setAttribute('role', 'alert');
    ov.style.cssText = 'position:fixed;inset:0;z-index:2147483647;background:#0f172a;color:#e2e8f0;' +
      'display:flex;align-items:center;justify-content:center;padding:24px;text-align:center;' +
      'font-family:-apple-system,system-ui,Segoe UI,Roboto,sans-serif';
    ov.innerHTML =
      '<div style="max-width:440px">' +
      '<div style="width:84px;height:84px;border-radius:50%;background:rgba(239,68,68,.12);display:flex;' +
      'align-items:center;justify-content:center;margin:0 auto 18px;font-size:40px">⛔</div>' +
      '<h2 style="margin:0 0 10px;font-size:20px">Acesso ao UC Talk bloqueado</h2>' +
      '<p style="margin:0 0 6px;color:#94a3b8;line-height:1.55;font-size:14px">' +
      'O acesso desta empresa ao UC Talk está suspenso. Mensagens do WhatsApp não estão sendo ' +
      'recebidas nem enviadas por aqui.</p>' +
      '<p style="margin:14px 0 0;color:#cbd5e1;font-size:14px">Para reativar, fale com a <b>UC Technology</b>.</p>' +
      '</div>';
    (document.body || document.documentElement).appendChild(ov);
  }

  window.fetch = function () {
    return original.apply(this, arguments).then(function (r) {
      if (r && r.status === 403 && !mostrado) {
        r.clone().json().then(function (j) {
          if (j && j.codigo === 'bloqueado') {
            if (document.body) mostrar();
            else document.addEventListener('DOMContentLoaded', mostrar);
          }
        }).catch(function () {});
      }
      return r;
    });
  };
})();
