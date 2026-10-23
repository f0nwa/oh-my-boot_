// ⌘K / Ctrl+K search: a dialog that asks /search for machines, installations,
// images, answer files and pages as you type. Arrows choose, Enter opens,
// Esc closes. Without JavaScript the header button opens the /search page.
(function () {
  var dialog = document.getElementById("search-dialog");
  if (!dialog || typeof dialog.showModal !== "function") return;
  var en = document.documentElement.lang === "en";
  var input = dialog.querySelector('input[name="q"]');
  var list = document.getElementById("search-results");
  var active = -1, timer = null, seq = 0;
  var mac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

  document.querySelectorAll("[data-search-open] kbd").forEach(function (kbd) { kbd.textContent = mac ? "⌘K" : "Ctrl K"; });

  function hint(text) {
    list.replaceChildren();
    var li = document.createElement("li");
    li.className = "search-hint";
    li.textContent = text;
    list.append(li);
    active = -1;
  }
  function options() { return list.querySelectorAll("a.search-item"); }
  function choose(index) {
    var items = options();
    if (!items.length) return;
    active = (index + items.length) % items.length;
    items.forEach(function (item, i) { item.setAttribute("aria-selected", i === active ? "true" : "false"); });
    items[active].scrollIntoView({ block: "nearest" });
  }
  function render(results) {
    list.replaceChildren();
    if (!results.length) { hint(en ? "Nothing found." : "Ничего не найдено."); return; }
    var kind = "";
    results.forEach(function (r) {
      if (r.kind !== kind) {
        kind = r.kind;
        var head = document.createElement("li");
        head.className = "search-group";
        head.textContent = kind;
        list.append(head);
      }
      var li = document.createElement("li");
      var a = document.createElement("a");
      a.className = "search-item";
      a.href = r.href;
      a.setAttribute("role", "option");
      var title = document.createElement("span");
      title.className = "search-title";
      title.textContent = r.title;
      a.append(title);
      if (r.sub) {
        var sub = document.createElement("small");
        sub.className = "mono";
        sub.textContent = r.sub;
        a.append(sub);
      }
      li.append(a);
      list.append(li);
    });
    choose(0);
  }
  function search() {
    var q = input.value.trim();
    if (!q) { hint(en ? "Type a MAC address, an image or an installation." : "Введите MAC, название образа или установки."); return; }
    var mine = ++seq;
    fetch("/search?format=json&q=" + encodeURIComponent(q), { credentials: "same-origin" })
      .then(function (res) { return res.ok ? res.json() : []; })
      .then(function (results) { if (mine === seq) render(results); })
      .catch(function () {});
  }
  function open(event) {
    if (event) event.preventDefault();
    if (!dialog.open) dialog.showModal();
    input.focus();
    input.select();
    search();
  }

  document.querySelectorAll("[data-search-open]").forEach(function (button) { button.addEventListener("click", open); });
  document.addEventListener("keydown", function (event) {
    var typing = /^(INPUT|TEXTAREA|SELECT)$/.test(event.target.tagName) || event.target.isContentEditable;
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") open(event);
    else if (event.key === "/" && !typing && !dialog.open) open(event);
  });
  input.addEventListener("input", function () { clearTimeout(timer); timer = setTimeout(search, 150); });
  dialog.addEventListener("keydown", function (event) {
    if (event.key === "ArrowDown") { event.preventDefault(); choose(active + 1); }
    else if (event.key === "ArrowUp") { event.preventDefault(); choose(active - 1); }
    else if (event.key === "Enter" && active >= 0) { event.preventDefault(); location.href = options()[active].href; }
  });
  // A click on the backdrop closes the dialog.
  dialog.addEventListener("click", function (event) { if (event.target === dialog) dialog.close(); });
})();
