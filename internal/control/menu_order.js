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
