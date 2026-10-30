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

  function update() {
    var profile = checked("boot_profile_id");
    var id = profile ? profile.dataset.windowsProfile : "";
    var windows = !!profile && profile.dataset.kind === "windows_iso";
    ["windows-drivers", "windows-answers"].forEach(function (id) {
      var group = document.getElementById(id);
      group.hidden = !windows;
      group.disabled = !windows;
    });
    ["summary-build-label", "summary-build"].forEach(function (id) { document.getElementById(id).hidden = !windows; });
    form.querySelectorAll('input[name="answer_id"][data-profile]').forEach(function (input) {
      var own = windows && input.dataset.profile === id;
      input.closest("label").hidden = !own;
      input.disabled = !own;
      if (!own && input.checked) form.querySelector('input[name="answer_id"][value="0"]').checked = true;
    });
    var answer = windows ? checked("answer_id") : null;
    if (!titleEdited) {
      var base = answer && answer.value !== "0" ? answer : profile;
      title.value = base ? base.dataset.title : "";
    }
    var drivers = windows && form.querySelectorAll('input[name="package"]:checked, input[name="inf"]:checked').length > 0;
    var wipes = answer && answer.dataset.wipes === "true";
    text("preview-title", (title.value || "Windows 11 x64"));
    text("preview-folder", folder.value ? " / " + folder.value.split("/").join(" / ") : "");
    text("preview-hint", wipes ? "!! WIPES DISK" : (drivers ? (en ? "drivers" : "драйверы") : ""));
    document.getElementById("preview-hint").classList.toggle("danger", !!wipes);
    text("summary-build", drivers ? (en ? "needed, a few minutes" : "нужна, несколько минут") : (en ? "not needed" : "не нужна"));
  }
  form.addEventListener("change", update);
  form.addEventListener("input", update);
  update();
})();
