(function () {
  "use strict";

  function snapshotForm(form) {
    var parts = [];
    var fields = form.querySelectorAll("input, select, textarea");
    for (var i = 0; i < fields.length; i++) {
      var el = fields[i];
      var name = el.getAttribute("name") || "";
      if (!name || name === "csrf" || name === "test_username" || name === "enable_now") {
        continue;
      }
      if (el.type === "checkbox" || el.type === "radio") {
        parts.push(name + "=" + (el.checked ? "1" : "0"));
      } else {
        parts.push(name + "=" + (el.value || ""));
      }
    }
    return parts.join("\x00");
  }

  function wireDirtySubmitGate(form) {
    if (!form) return null;
    var saveBtns = form.querySelectorAll("[data-integration-save]");
    if (!saveBtns.length) return null;
    var initial = snapshotForm(form);

    function sync() {
      var dirty = snapshotForm(form) !== initial;
      for (var i = 0; i < saveBtns.length; i++) {
        saveBtns[i].disabled = !dirty;
      }
    }

    form.addEventListener("input", sync);
    form.addEventListener("change", sync);
    form.addEventListener("submit", function (ev) {
      sync();
      if (saveBtns[0].disabled) {
        ev.preventDefault();
      }
    });

    sync();
    return {
      reset: function () {
        if (typeof form.reset === "function") {
          form.reset();
        }
        initial = snapshotForm(form);
        sync();
      }
    };
  }

  function wireDialog(openAttr, dialogId, gate) {
    var dialog = document.getElementById(dialogId);
    if (!dialog) return;
    document.querySelectorAll("[" + openAttr + "]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        if (gate) gate.reset();
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

  var ldapGate = wireDirtySubmitGate(document.getElementById("ldap-config-form"));
  var kcGate = wireDirtySubmitGate(document.getElementById("keycloak-config-form"));
  wireDialog("data-ldap-config-open", "ldap-config-dialog", ldapGate);
  wireDialog("data-keycloak-config-open", "keycloak-config-dialog", kcGate);
  wireTestButton("ldap-test-btn", "ldap-config-form", "/integrations/ldap/test", "ldap-test-result");
  wireTestButton("keycloak-test-btn", "keycloak-config-form", "/integrations/keycloak/test", "keycloak-test-result");
})();
