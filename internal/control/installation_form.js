(function () {
  var form = document.getElementById("installation-form");
  if (!form) return;
  var en = document.documentElement.lang === "en";
  var profile = form.querySelector('select[name="profile_id"]');
  var answer = form.querySelector('select[name="answer_id"]');
  var title = form.querySelector('input[name="title"]');
  var warning = document.getElementById("answer-wipes");
  var titleEdited = title.value !== "";
  title.addEventListener("input", function () { titleEdited = true; });
  function update() {
    var id = profile.value;
    Array.prototype.forEach.call(answer.options, function (option) {
      var own = !option.dataset.profile || option.dataset.profile === id;
      option.hidden = !own;
      option.disabled = !own;
      if (!own && option.selected) answer.value = "0";
    });
    var chosen = answer.options[answer.selectedIndex];
    warning.hidden = !(chosen && chosen.dataset.wipes === "true");
    if (!titleEdited) {
      var base = chosen && chosen.value !== "0" ? chosen : profile.options[profile.selectedIndex];
      title.value = base ? base.dataset.title : "";
    }
  }
  warning.textContent = en ? "This answer file wipes the disk without asking." : "Этот файл ответов очищает диск без вопросов.";
  profile.addEventListener("change", update);
  answer.addEventListener("change", update);
  update();
})();
