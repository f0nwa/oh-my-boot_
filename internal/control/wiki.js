(function () {
  var en = document.documentElement.lang === "en";
  var copyLabel = en ? "Copy" : "Скопировать";
  var copiedLabel = en ? "Copied" : "Скопировано";

  // 1. Live sidebar tree filter + Enter submits full-text search to /wiki?q=...
  var filterInput = document.querySelector("[data-wiki-filter]");
  if (filterInput) {
    var groups = document.querySelectorAll("[data-wiki-section]");
    filterInput.addEventListener("input", function () {
      var q = filterInput.value.trim().toLowerCase();
      groups.forEach(function (group) {
        var items = group.querySelectorAll("[data-wiki-item]");
        var visible = 0;
        items.forEach(function (li) {
          var hay = (li.getAttribute("data-wiki-search") || li.textContent || "").toLowerCase();
          var match = !q || hay.indexOf(q) !== -1;
          li.hidden = !match;
          if (match) visible++;
        });
        group.hidden = visible === 0;
      });
    });
  }

  // 2. Copy code blocks with clipboard API and hidden textarea fallback
  function copyText(text) {
    if (navigator.clipboard && typeof navigator.clipboard.writeText === "function") {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      try {
        var ta = document.createElement("textarea");
        ta.value = text;
        ta.setAttribute("readonly", "");
        ta.style.position = "fixed";
        ta.style.opacity = "0";
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        ta.remove();
        resolve();
      } catch (err) {
        reject(err);
      }
    });
  }

  document.querySelectorAll("[data-wiki-copy]").forEach(function (btn) {
    btn.textContent = copyLabel;
    btn.addEventListener("click", function () {
      var block = btn.closest(".wiki-code-block");
      var code = block ? block.querySelector("pre code") : null;
      if (!code) return;
      copyText(code.textContent || "").then(function () {
        btn.textContent = copiedLabel;
        btn.classList.add("is-copied");
        setTimeout(function () {
          btn.textContent = copyLabel;
          btn.classList.remove("is-copied");
        }, 1400);
      }).catch(function () {});
    });
  });

  // 3. Highlight active heading in right-hand TOC on scroll
  var tocLinks = Array.prototype.slice.call(document.querySelectorAll(".wiki-toc a[href^='#']"));
  if (tocLinks.length && typeof IntersectionObserver === "function") {
    var linkById = {};
    var headings = [];
    tocLinks.forEach(function (a) {
      var id = a.getAttribute("href").slice(1);
      var el = document.getElementById(id);
      if (el) {
        linkById[id] = a;
        headings.push(el);
      }
    });
    function setActive(id) {
      tocLinks.forEach(function (a) {
        a.classList.toggle("active", a === linkById[id]);
      });
    }
    if (headings.length) {
      setActive(headings[0].id);
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (entry) {
          if (entry.isIntersecting) setActive(entry.target.id);
        });
      }, { rootMargin: "-72px 0px -70% 0px", threshold: 0 });
      headings.forEach(function (h) { io.observe(h); });
    }
  }
})();
