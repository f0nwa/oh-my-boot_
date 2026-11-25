// Drag menu items by their ⋮⋮ handle (or move them with the arrow keys on the
// handle) to change the order of the draft. Items move only among the lines of
// the same screen; the new order is saved and the page reloads.
(function () {
  var list = document.getElementById("menu-items");
  if (!list) return;
  var csrf = list.dataset.csrf;
  var dragged = null;

  function keys() {
    return Array.prototype.map.call(list.querySelectorAll("[data-key]"), function (line) { return line.dataset.key; });
  }
  function save(before) {
    var after = keys();
    if (after.join(",") === before.join(",")) return;
    var data = new URLSearchParams();
    data.append("csrf", csrf);
    after.forEach(function (key) { data.append("key", key); });
    fetch("/menu/order", { method: "POST", body: data, credentials: "same-origin" }).then(function () { location.reload(); });
  }
  function sibling(line, up) {
    var next = up ? line.previousElementSibling : line.nextElementSibling;
    // Skip folders and the items of deeper screens between two siblings.
    while (next && next.dataset.parent !== line.dataset.parent) {
      next = up ? next.previousElementSibling : next.nextElementSibling;
    }
    return next;
  }

  list.querySelectorAll(".drag-handle").forEach(function (handle) {
    var line = handle.closest("[data-key]");
    handle.addEventListener("pointerdown", function () { line.draggable = true; });
    handle.addEventListener("keydown", function (event) {
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
      event.preventDefault();
      var before = keys();
      var other = sibling(line, event.key === "ArrowUp");
      if (!other) return;
      if (event.key === "ArrowUp") list.insertBefore(line, other); else list.insertBefore(line, other.nextElementSibling);
      handle.focus();
      save(before);
    });
  });

  var start = [];
  list.addEventListener("dragstart", function (event) {
    dragged = event.target.closest("[data-key]");
    if (!dragged) return;
    start = keys();
    dragged.classList.add("dragging");
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", dragged.dataset.key);
  });
  list.addEventListener("dragover", function (event) {
    if (!dragged) return;
    var line = event.target.closest("[data-key]");
    if (!line || line === dragged || line.dataset.parent !== dragged.dataset.parent) return;
    event.preventDefault();
    var box = line.getBoundingClientRect();
    list.insertBefore(dragged, event.clientY < box.top + box.height / 2 ? line : line.nextElementSibling);
  });
  list.addEventListener("drop", function (event) { event.preventDefault(); });
  list.addEventListener("dragend", function () {
    if (!dragged) return;
    dragged.classList.remove("dragging");
    dragged.draggable = false;
    dragged = null;
    save(start);
  });
})();

// Live ASCII validation for menu inspector forms (item title/folder and Diagnostics).
(function () {
  var fields = document.querySelectorAll(".inspector [data-ascii-field]");
  if (!fields.length) return;
  var en = document.documentElement.lang === "en";
  var asciiRe = /^[\x20-\x7E]*$/;

  function validateValue(val, kind) {
    if (!asciiRe.test(val)) {
      return en
        ? "Use Latin letters, digits, and basic punctuation — the boot console cannot show Cyrillic"
        : "Только латиница, цифры и базовые знаки — консоль загрузки не показывает кириллицу";
    }
    if (kind === "folder") {
      var trimmed = val.trim();
      if (trimmed !== "") {
        var parts = trimmed.split("/");
        for (var i = 0; i < parts.length; i++) {
          var seg = parts[i].trim();
          if (!seg || seg === "." || seg === "..") {
            return en
              ? "Invalid folder path (use names separated by “/”, without “..” or empty segments)"
              : "Некорректный путь каталога (используйте имена через «/», без «..» и пустых сегментов)";
          }
        }
      }
    }
    return "";
  }

  var forms = [];
  fields.forEach(function (input) {
    var form = input.form;
    if (form && forms.indexOf(form) === -1) forms.push(form);
  });

  forms.forEach(function (form) {
    var formFields = Array.prototype.slice.call(form.querySelectorAll("[data-ascii-field]"));
    var saveBtn = form.querySelector('button:not([name="inherit_title"])');

    function checkForm() {
      var firstBad = null;
      formFields.forEach(function (input) {
        var msg = validateValue(input.value, input.dataset.asciiField);
        input.classList.toggle("is-invalid", !!msg);
        input.setAttribute("aria-invalid", msg ? "true" : "false");
        var errId = input.id ? input.id + "-error" : "";
        var errEl = errId ? document.getElementById(errId) : null;
        if (errEl) {
          errEl.textContent = msg;
          errEl.hidden = !msg;
        }
        if (msg && !firstBad) firstBad = input;
      });
      if (saveBtn) saveBtn.disabled = !!firstBad;
      return firstBad;
    }

    form.addEventListener("input", checkForm);
    form.addEventListener("submit", function (e) {
      if (e.submitter && e.submitter.name === "inherit_title") return;
      var bad = checkForm();
      if (bad) {
        e.preventDefault();
        bad.focus();
      }
    });
    checkForm();
  });
})();
