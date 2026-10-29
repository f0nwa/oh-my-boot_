(function () {
  var form = document.querySelector(".theme-form");
  if (!form) return;
  form.addEventListener("change", function (event) {
    if (event.target.name === "theme") form.requestSubmit();
  });
  form.classList.add("theme-ready");
})();
