// Page notices: fade in (CSS), hide by the close button or, for regular
// messages, when the 30-second timer bar runs out. Hovering pauses the timer.
(function () {
  "use strict";

  function hide(notice) {
    if (notice.classList.contains("is-hiding")) return;
    notice.style.maxHeight = notice.scrollHeight + "px";
    // Force layout so the transition starts from the current height.
    void notice.offsetHeight;
    notice.classList.add("is-hiding");
    var removed = false;
    function remove() {
      if (removed) return;
      removed = true;
      notice.remove();
    }
    notice.addEventListener("transitionend", function (event) {
      if (event.target === notice && event.propertyName === "max-height") remove();
    });
    setTimeout(remove, 600);
  }

  function dropMessageParam() {
    if (!window.history || !window.history.replaceState) return;
    var url = new URL(window.location.href);
    if (!url.searchParams.has("message")) return;
    url.searchParams.delete("message");
    window.history.replaceState(window.history.state, "", url.pathname + url.search + url.hash);
  }

  function init(notice) {
    var close = notice.querySelector("[data-notice-close]");
    if (close) close.addEventListener("click", function () { hide(notice); });
    if (!notice.hasAttribute("data-notice-auto")) return;
    var timer = notice.querySelector(".notice-timer");
    if (timer) {
      timer.addEventListener("animationend", function () { hide(notice); });
    }
  }

  function start() {
    var notices = document.querySelectorAll("[data-notice]");
    for (var i = 0; i < notices.length; i++) init(notices[i]);
    // A reload should not show the same message again.
    if (document.querySelector("[data-notice-auto]")) dropMessageParam();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", start);
  } else {
    start();
  }
})();
