// New installation form: shows only the answer files of the chosen image,
// fills the menu title from the choice until the user types one, and keeps
// the screen preview and the build summary in step with the form.
(function () {
  var form = document.getElementById("installation-form");
  if (!form) return;
  var en = document.documentElement.lang === "en";
  var title = form.querySelector('input[name="title"]');
  var folder = form.querySelector('input[name="folder"]');
  var titleEdited = title.value !== "";
  title.addEventListener("input", function () { titleEdited = true; });

  function checked(name) { return form.querySelector('input[name="' + name + '"]:checked'); }
  function text(id, value) { var el = document.getElementById(id); if (el) el.textContent = value; }
  function syncPackages() {
    form.querySelectorAll('input[name="package"]').forEach(function (pkg) {
      var infs = form.querySelectorAll('input[name="inf"][data-package="' + pkg.value + '"]');
      var selected = 0;
      infs.forEach(function (inf) { if (inf.checked) selected++; });
      pkg.checked = infs.length > 0 && selected === infs.length;
      pkg.indeterminate = selected > 0 && selected < infs.length;
    });
  }

  function update(e) {
    if (e && e.target && e.target.name === "package") {
      form.querySelectorAll('input[name="inf"][data-package="' + e.target.value + '"]').forEach(function (inf) {
        inf.checked = e.target.checked;
      });
    }
    syncPackages();
    var profile = checked("boot_profile_id");
    var id = profile ? profile.dataset.windowsProfile : "";
    var windows = !!profile && profile.dataset.kind === "windows_iso";
    var winpe = windows || !!profile && profile.dataset.kind === "winpe_iso";
    ["windows-drivers", "windows-answers"].forEach(function (id) {
      var group = document.getElementById(id);
      var available = id === "windows-drivers" ? winpe : windows;
      group.hidden = !available;
      group.disabled = !available;
    });
    var ubuntu = document.getElementById("ubuntu-autoinstall");
    ubuntu.hidden = !profile || (profile.dataset.kind !== "ubuntu_builtin" && profile.dataset.kind !== "ubuntu_iso");
    ubuntu.disabled = ubuntu.hidden;
    ["summary-build-label", "summary-build"].forEach(function (id) { document.getElementById(id).hidden = !winpe; });
    form.querySelectorAll('input[name="answer_id"][data-profile]').forEach(function (input) {
      var own = windows && input.dataset.profile === id;
      input.closest("label").hidden = !own;
      input.disabled = !own;
      if (!own && input.checked) form.querySelector('input[name="answer_id"][value="0"]').checked = true;
    });
    function filterAnswers(name, searchID, eligible) {
      var search = document.getElementById(searchID);
      var items = Array.from(form.querySelectorAll('input[name="' + name + '"]')).filter(function (input) { return input.value !== "0" && eligible(input); });
      search.hidden = items.length <= 10;
      var query = search.hidden ? "" : search.value.trim().toLocaleLowerCase();
      items.forEach(function (input) {
        input.closest("label").hidden = !!query && !input.dataset.title.toLocaleLowerCase().includes(query) && !input.checked;
      });
    }
    filterAnswers("answer_id", "windows-answer-search", function (input) { return !input.disabled; });
    filterAnswers("autoinstall_id", "ubuntu-answer-search", function () { return true; });
    text("menu-step-no", windows ? "4" : winpe ? "3" : !ubuntu.hidden ? "3" : "2");
    var answer = windows ? checked("answer_id") : null;
    var autoinstall = !ubuntu.disabled ? checked("autoinstall_id") : null;
    if (!titleEdited) {
      var base = answer && answer.value !== "0" ? answer : profile;
      title.value = base ? base.dataset.title : "";
    }
    var drivers = winpe && form.querySelectorAll('input[name="package"]:checked, input[name="inf"]:checked').length > 0;
    var wipes = (answer && answer.dataset.wipes === "true") || (autoinstall && autoinstall.dataset.wipes === "true");
    text("preview-title", (title.value || "Windows 11 x64"));
    text("preview-folder", folder.value ? " / " + folder.value.split("/").join(" / ") : "");
    text("preview-hint", wipes ? "!! WIPES DISK" : (drivers ? (en ? "drivers" : "драйверы") : ""));
    document.getElementById("preview-hint").classList.toggle("danger", !!wipes);
    text("summary-build", (drivers || profile && profile.dataset.prepare === "true") ? (en ? "needed, a few minutes" : "нужна, несколько минут") : (en ? "not needed" : "не нужна"));
  }
  form.addEventListener("change", update);
  form.addEventListener("input", update);
  form.addEventListener("submit", function () {
    var button = form.querySelector('button[type="submit"], button.btn-primary');
    if (button) { button.disabled = true; button.textContent = en ? "Preparing…" : "Подготавливается…"; }
  });
  update();
})();
