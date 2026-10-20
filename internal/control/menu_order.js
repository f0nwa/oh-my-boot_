(function () {
  var table = document.getElementById("menu-items");
  if (!table) return;
  var body = table.tBodies[0];
  var csrf = table.dataset.csrf;
  var dragged = null;

  function keys() {
    return Array.prototype.map.call(body.querySelectorAll("tr[data-key]"), function (row) { return row.dataset.key; });
  }
  function save(before) {
    var after = keys();
    if (after.join(",") === before.join(",")) return;
    var data = new URLSearchParams();
    data.append("csrf", csrf);
    after.forEach(function (key) { data.append("key", key); });
    fetch("/menu/order", { method: "POST", body: data, credentials: "same-origin" }).then(function () { location.reload(); });
  }

  body.querySelectorAll(".drag-handle").forEach(function (handle) {
    var row = handle.closest("tr");
    handle.addEventListener("pointerdown", function () { row.draggable = true; });
    handle.addEventListener("keydown", function (event) {
      if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
      event.preventDefault();
      var before = keys();
      var sibling = event.key === "ArrowUp" ? row.previousElementSibling : row.nextElementSibling;
      if (!sibling || !sibling.dataset.key) return;
      if (event.key === "ArrowUp") body.insertBefore(row, sibling); else body.insertBefore(sibling, row);
      handle.focus();
      save(before);
    });
  });

  var start = [];
  body.addEventListener("dragstart", function (event) {
    dragged = event.target.closest("tr[data-key]");
    if (!dragged) return;
    start = keys();
    dragged.classList.add("dragging");
    event.dataTransfer.effectAllowed = "move";
    event.dataTransfer.setData("text/plain", dragged.dataset.key);
  });
  body.addEventListener("dragover", function (event) {
    if (!dragged) return;
    var row = event.target.closest("tr[data-key]");
    if (!row || row === dragged) return;
    event.preventDefault();
    var box = row.getBoundingClientRect();
    body.insertBefore(dragged, event.clientY < box.top + box.height / 2 ? row : row.nextElementSibling);
  });
  body.addEventListener("drop", function (event) { event.preventDefault(); });
  body.addEventListener("dragend", function () {
    if (!dragged) return;
    dragged.classList.remove("dragging");
    dragged.draggable = false;
    dragged = null;
    save(start);
  });

  // Title and folder save when the field loses focus or Enter is pressed.
  table.querySelectorAll("input.cell-input").forEach(function (input) {
    var initial = input.value;
    input.addEventListener("change", function () {
      if (input.value !== initial) document.getElementById(input.getAttribute("form")).requestSubmit();
    });
  });
})();
