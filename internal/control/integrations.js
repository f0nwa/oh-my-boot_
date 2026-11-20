(function () {
  "use strict";

  function wireDialog(openAttr, dialogId) {
    var dialog = document.getElementById(dialogId);
    if (!dialog) return;
    document.querySelectorAll("[" + openAttr + "]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (typeof dialog.showModal === "function") {
          dialog.showModal();
        } else {
          dialog.setAttribute("open", "");
        }
      });
    });
    dialog.querySelectorAll("[data-dialog-close]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (typeof dialog.close === "function") {
          dialog.close();
        } else {
          dialog.removeAttribute("open");
        }
      });
    });
    dialog.addEventListener("click", function (ev) {
      if (ev.target !== dialog) return;
      var rect = dialog.getBoundingClientRect();
      var inside =
        ev.clientX >= rect.left &&
        ev.clientX <= rect.right &&
        ev.clientY >= rect.top &&
        ev.clientY <= rect.bottom;
      if (!inside && typeof dialog.close === "function") {
        dialog.close();
      }
    });
  }

  function wireTestButton(btnId, formId, endpoint, outId) {
    var btn = document.getElementById(btnId);
    var form = document.getElementById(formId);
    var out = document.getElementById(outId);
    if (!btn || !form || !out) return;
    btn.addEventListener("click", function () {
      var fd = new FormData(form);
      var body = new URLSearchParams();
      fd.forEach(function (val, key) {
        body.append(key, String(val));
      });
      btn.disabled = true;
      out.hidden = false;
      out.className = "integration-test-result";
      out.textContent = btn.getAttribute("data-testing-label") || "Проверка…";
      fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8" },
        body: body.toString(),
        credentials: "same-origin"
      })
        .then(function (resp) {
          return resp.json().catch(function () {
            return { ok: false, detail: "HTTP " + resp.status };
          });
        })
        .then(function (data) {
          out.className = data.ok
            ? "integration-test-result is-ok"
            : "integration-test-result is-error";
          out.textContent = data.detail || (data.ok ? "OK" : "Ошибка");
        })
        .catch(function (err) {
          out.className = "integration-test-result is-error";
          out.textContent = String(err);
        })
        .finally(function () {
          btn.disabled = false;
        });
    });
  }

  wireDialog("data-ldap-config-open", "ldap-config-dialog");
  wireDialog("data-keycloak-config-open", "keycloak-config-dialog");
  wireTestButton("ldap-test-btn", "ldap-config-form", "/integrations/ldap/test", "ldap-test-result");
  wireTestButton("keycloak-test-btn", "keycloak-config-form", "/integrations/keycloak/test", "keycloak-test-result");
})();
