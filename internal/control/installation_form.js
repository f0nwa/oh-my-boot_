// Installation creation & edit form: evaluates Windows answer compatibility live
// against the selected image's install.wim metadata, sorts scripts, enforces warning
// confirmation, keeps steps capped at 5 visible items with expand/collapse, and
// activates per-step search only when more than 5 items exist.
(function () {
  var form = document.getElementById("installation-form");
  if (!form) return;
  var en = document.documentElement.lang === "en";
  var title = form.querySelector('input[name="title"]');
  var folder = form.querySelector('input[name="folder"]');
  var titleError = document.getElementById("i-title-error");
  var folderError = document.getElementById("i-folder-error");
  var asciiRe = /^[\x20-\x7E]*$/;
  var submitBtn = document.getElementById("install-submit") || form.querySelector('button[type="submit"], button.btn-primary');
  var defaultSubmitLabel = submitBtn ? submitBtn.textContent : "";
  var titleEdited = title.value !== "";
  title.addEventListener("input", function () { titleEdited = true; });

  function validateMenuText(val, isFolder) {
    if (!asciiRe.test(val)) {
      return en
        ? "Use Latin letters, digits, and basic punctuation — the boot console cannot show Cyrillic"
        : "Только латиница, цифры и базовые знаки — консоль загрузки не показывает кириллицу";
    }
    if (isFolder) {
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

  function applyFieldValidation(input, errEl, isFolder) {
    if (!input) return true;
    var msg = validateMenuText(input.value, isFolder);
    input.classList.toggle("is-invalid", !!msg);
    input.setAttribute("aria-invalid", msg ? "true" : "false");
    if (errEl) {
      errEl.textContent = msg;
      errEl.hidden = !msg;
    }
    return !msg;
  }

  function composeMenuTitle(osTitle, scriptTitle) {
    osTitle = (osTitle || "").trim();
    scriptTitle = (scriptTitle || "").trim();
    if (!osTitle) return scriptTitle;
    if (!scriptTitle) return osTitle;

    var cleanScript = scriptTitle;
    var osWords = osTitle.split(/\s+/);
    var prefixes = [osTitle.toLowerCase()];
    if (osWords.length >= 2) {
      prefixes.push((osWords[0] + " " + osWords[1]).toLowerCase());
    }
    prefixes.push(osWords[0].toLowerCase());

    for (var i = 0; i < prefixes.length; i++) {
      var p = prefixes[i];
      if (cleanScript.toLowerCase().indexOf(p) === 0) {
        cleanScript = cleanScript.slice(p.length).replace(/^[\s\-–—:_/]+/, "").trim();
        break;
      }
    }

    if (!cleanScript) return osTitle;
    var combined = osTitle + " - " + cleanScript;
    if (combined.length > 80) {
      combined = combined.slice(0, 80).trim();
    }
    return combined;
  }

  var EDITION_ALIASES = {
    home: ["Core"],
    home_n: ["CoreN"],
    home_single: ["CoreSingleLanguage"],
    education: ["Education"],
    education_n: ["EducationN"],
    pro: ["Professional"],
    pro_n: ["ProfessionalN"],
    pro_education: ["ProfessionalEducation"],
    pro_education_n: ["ProfessionalEducationN"],
    pro_workstations: ["ProfessionalWorkstation"],
    pro_workstations_n: ["ProfessionalWorkstationN"],
    enterprise: ["Enterprise"],
    enterprise_n: ["EnterpriseN"]
  };

  var expandedSteps = {};

  function checked(name) { return form.querySelector('input[name="' + name + '"]:checked'); }
  function text(id, value) { var el = document.getElementById(id); if (el) el.textContent = value; }
  function parseJSON(str, fallback) {
    if (!str) return fallback;
    try { return JSON.parse(str); } catch (_) { return fallback; }
  }
  function containsFold(list, val) {
    var want = (val || "").trim().toLowerCase();
    if (!want || !list || !list.length) return false;
    return list.some(function (x) { return (x || "").trim().toLowerCase() === want; });
  }
  function editionMatches(targetEdition, imgEdition) {
    var want = (targetEdition || "").trim().toLowerCase();
    var have = (imgEdition || "").trim().toLowerCase();
    if (!want || !have) return false;
    if (want === have) return true;
    var aliases = EDITION_ALIASES[want] || [];
    return aliases.some(function (a) { return a.toLowerCase() === have; });
  }
  function uniqueNonEmpty(list) {
    var seen = {}, out = [];
    list.forEach(function (v) {
      var s = (v || "").trim();
      var k = s.toLowerCase();
      if (s && !seen[k]) { seen[k] = true; out.push(s); }
    });
    return out;
  }

  function evaluateCompatibility(target, wim) {
    var res = { status: "compatible", errors: [], warnings: [], summary: "" };
    if (!wim || !wim.known || !wim.images || !wim.images.length) {
      return res;
    }
    if ((target.partition_layout || "").toUpperCase() === "MBR") {
      res.warnings.push(en
        ? "Script specifies MBR partitioning; UEFI network boot installation will fail"
        : "В скрипте выбрана разметка MBR; при сетевой загрузке в режиме UEFI установка завершится ошибкой");
    }

    var candidates = wim.images;
    var archs = target.architectures || [];
    if (archs.length) {
      var archMatched = candidates.filter(function (img) { return containsFold(archs, img.architecture); });
      if (!archMatched.length) {
        res.status = "incompatible";
        var haveArchs = uniqueNonEmpty(wim.images.map(function (img) { return img.architecture; })).join(", ");
        res.errors.push(en
          ? "Script targets " + archs.join(", ") + ", image has " + haveArchs
          : "Скрипт рассчитан на " + archs.join(", ") + ", а в образе — " + haveArchs);
        return res;
      }
      candidates = archMatched;
    }

    var matched = candidates;
    var mode = target.image_mode || "any";
    if (mode === "index") {
      matched = candidates.filter(function (img) { return img.index === target.image_index; });
      if (!matched.length) {
        res.status = "incompatible";
        res.errors.push(en
          ? "Image index " + target.image_index + " not found (total editions: " + wim.images.length + ")"
          : "В образе нет индекса " + target.image_index + " (всего редакций: " + wim.images.length + ")");
        return res;
      }
    } else if (mode === "name") {
      var wantName = (target.image_name || "").trim().toLowerCase();
      matched = candidates.filter(function (img) {
        return (img.name || "").trim().toLowerCase() === wantName ||
               (img.display_name || "").trim().toLowerCase() === wantName;
      });
      if (!matched.length) {
        res.status = "incompatible";
        res.errors.push(en
          ? "Edition “" + target.image_name + "” not found in image"
          : "В образе нет редакции «" + target.image_name + "»");
        return res;
      }
    } else if (mode === "edition") {
      matched = candidates.filter(function (img) { return editionMatches(target.edition_id, img.edition_id); });
      if (!matched.length) {
        var edLabel = target.edition_label || target.edition_id;
        var avail = uniqueNonEmpty(candidates.map(function (img) { return img.edition_id; })).join(", ");
        res.status = "incompatible";
        res.errors.push(en
          ? "Edition " + edLabel + " not found in image (available: " + avail + ")"
          : "В образе нет редакции " + edLabel + " (доступно: " + avail + ")");
        return res;
      }
    }

    if (target.win11_only) {
      var knownBuilds = matched.filter(function (img) { return (img.build || 0) > 0; });
      if (knownBuilds.length && knownBuilds.every(function (img) { return img.build < 22000; })) {
        res.warnings.push(en
          ? "Script enables Windows 11 requirement bypasses (TPM/Secure Boot/RAM), while image is build " + knownBuilds[0].build + " (Windows 10)"
          : "Скрипт содержит обход требований Windows 11 (TPM/Secure Boot/RAM), а выбранный образ имеет build " + knownBuilds[0].build + " (Windows 10)");
      }
    }

    var targetLang = (target.image_language || "").trim();
    if (targetLang) {
      var hasLangMeta = matched.some(function (img) { return img.languages && img.languages.length; });
      if (hasLangMeta) {
        var hasLang = matched.some(function (img) {
          return containsFold(img.languages, targetLang) ||
                 (img.default_language || "").trim().toLowerCase() === targetLang.toLowerCase();
        });
        if (!hasLang) {
          var availLangs = uniqueNonEmpty([].concat.apply([], matched.map(function (img) { return img.languages || []; }))).join(", ");
          res.warnings.push(en
            ? "Script language " + targetLang + " not found in image (available: " + availLangs + ")"
            : "Язык сценария " + targetLang + " не найден в образе (доступны: " + availLangs + ")");
        }
      }
    }

    if (matched.length === 1) {
      var m = matched[0];
      var mName = (m.display_name || m.name || m.edition_id || "").trim();
      res.summary = en
        ? "Index " + m.index + " · " + mName + " · " + m.architecture
        : "Индекс " + m.index + " · " + mName + " · " + m.architecture;
    } else {
      res.summary = en
        ? "Matching editions in image: " + matched.length
        : "Подходит редакций в образе: " + matched.length;
    }
    if (res.warnings.length) res.status = "warning";
    return res;
  }

  function syncPackages() {
    form.querySelectorAll('input[name="package"]').forEach(function (pkg) {
      var infs = form.querySelectorAll('input[name="inf"][data-package="' + pkg.value + '"]');
      var selected = 0;
      infs.forEach(function (inf) { if (inf.checked) selected++; });
      pkg.checked = infs.length > 0 && selected === infs.length;
      pkg.indeterminate = selected > 0 && selected < infs.length;
    });
  }

  function updateStepVisibility(stepKey, searchID) {
    var list = form.querySelector('[data-step-list="' + stepKey + '"]');
    if (!list) return;
    var items = Array.from(list.querySelectorAll('[data-step-item="' + stepKey + '"]'));
    var search = document.getElementById(searchID);
    var moreBtn = form.querySelector('[data-step-more="' + stepKey + '"]');
    var hasMany = items.length > 5;
    if (search) {
      search.hidden = !hasMany;
      search.disabled = !hasMany;
    }
    var query = (search && !search.disabled) ? search.value.trim().toLowerCase() : "";
    var expanded = !!expandedSteps[stepKey];

    var hiddenCount = 0;
    items.forEach(function (item, idx) {
      var isChecked = !!item.querySelector('input:checked');
      var haystack = ((item.dataset.search || "") + " " + (item.textContent || "")).toLowerCase();
      var show;
      if (query) {
        show = haystack.indexOf(query) !== -1 || isChecked;
      } else {
        show = expanded || idx < 5 || isChecked;
      }
      item.hidden = !show;
      if (!show) hiddenCount++;
    });

    if (moreBtn) {
      if (!hasMany || query) {
        moreBtn.hidden = true;
      } else if (!expanded && hiddenCount === 0) {
        moreBtn.hidden = true;
      } else {
        moreBtn.hidden = false;
        moreBtn.textContent = expanded
          ? (moreBtn.dataset.lessLabel || (en ? "Show fewer" : "Свернуть"))
          : ((moreBtn.dataset.moreLabel || (en ? "Show more" : "Показать ещё")) + " " + hiddenCount);
      }
    }
  }

  function updateWindowsCompatibility(windows, profile) {
    var wim = (windows && profile) ? parseJSON(profile.dataset.wim, null) : null;
    var winProfileID = profile ? (profile.dataset.windowsProfile || "") : "";
    var wimKnown = !!(wim && wim.known && wim.images && wim.images.length);
    var unknownNote = document.getElementById("windows-wim-unknown-note");
    if (unknownNote) unknownNote.hidden = !windows || wimKnown;

    var list = form.querySelector('[data-step-list="windows-answers"]');
    if (!list) return;
    var items = Array.from(list.querySelectorAll('[data-step-item="windows-answers"]'));
    var defaultChoice = list.querySelector('[data-default-script]');

    var scored = items.map(function (item) {
      var input = item.querySelector('input[name="answer_id"]');
      var target = parseJSON(item.dataset.target, {});
      var checks = parseJSON(item.dataset.checks, {});
      var check = (winProfileID && checks && checks[winProfileID]) ? checks[winProfileID] : null;
      var compat = evaluateCompatibility(target, wim);

      var verifiedMatch = !!(check && check.result === "success" && check.sha256 === input.dataset.sha);
      var verifiedStale = !!(check && check.sha256 && check.sha256 !== input.dataset.sha);
      var verifiedFail = !!(check && check.result === "failure" && check.sha256 === input.dataset.sha);

      var badge = item.querySelector('[data-compat-badge]');
      var note = item.querySelector('[data-compat-note]');
      var checkNote = item.querySelector('[data-check-note]');

      item.classList.remove("choice-warn", "choice-off");
      input.disabled = !windows;

      if (wimKnown) {
        if (compat.status === "incompatible") {
          item.classList.add("choice-off");
          if (windows) input.disabled = true;
          if (input.checked) {
            var fallback = list.querySelector('input[name="answer_id"][value="0"]');
            if (fallback) fallback.checked = true;
          }
        } else if (compat.status === "warning") {
          item.classList.add("choice-warn");
        }
        if (badge) {
          badge.hidden = false;
          badge.className = "compat-badge compat-badge-" + compat.status;
          if (compat.status === "compatible") {
            badge.textContent = en ? "Compatible" : "Совместим";
          } else if (compat.status === "warning") {
            badge.textContent = en ? "Warning" : "Есть предупреждение";
          } else {
            badge.textContent = en ? "Incompatible" : "Несовместим";
          }
        }
        if (note) {
          var noteText = compat.status === "incompatible"
            ? compat.errors.join("; ")
            : (compat.status === "warning" ? compat.warnings.join("; ") : compat.summary);
          note.textContent = noteText;
          note.hidden = !noteText;
        }
      } else {
        if (badge) badge.hidden = true;
        if (note) { note.textContent = ""; note.hidden = true; }
      }

      if (checkNote) {
        if (verifiedMatch) {
          var dt = check.created_at ? check.created_at.slice(0, 10) : "";
          checkNote.textContent = (en ? "Verified on this image" : "Проверено на этом образе") + (dt ? " · " + dt : "") + (check.author ? " · " + check.author : "");
          checkNote.className = "compat-check-note compat-check-ok";
          checkNote.hidden = false;
        } else if (verifiedFail) {
          checkNote.textContent = en ? "Previous test on this image failed" : "Ранее была ошибка установки на этом образе";
          checkNote.className = "compat-check-note compat-check-bad";
          checkNote.hidden = false;
        } else if (verifiedStale) {
          checkNote.textContent = en ? "Script changed after previous check on this image" : "Скрипт изменился после проверки на этом образе";
          checkNote.className = "compat-check-note compat-check-warn";
          checkNote.hidden = false;
        } else {
          checkNote.textContent = "";
          checkNote.hidden = true;
        }
      }

      var rank = 1;
      if (wimKnown) {
        if (compat.status === "incompatible") rank = 3;
        else if (compat.status === "warning") rank = 2;
        else rank = verifiedMatch ? 0 : 1;
      } else {
        rank = verifiedMatch ? 0 : 1;
      }
      return { item: item, input: input, compat: compat, rank: rank, orig: parseInt(item.dataset.origIndex || "0", 10) };
    });

    scored.sort(function (a, b) {
      if (a.rank !== b.rank) return a.rank - b.rank;
      return a.orig - b.orig;
    });
    if (defaultChoice && list.firstElementChild !== defaultChoice) {
      list.insertBefore(defaultChoice, list.firstChild);
    }
    scored.forEach(function (entry) { list.appendChild(entry.item); });

    // Warning confirmation block for the currently selected script
    var warnBox = document.getElementById("windows-answer-warnings");
    var warnList = document.getElementById("windows-warning-list");
    var warnCheck = warnBox ? warnBox.querySelector('input[name="confirm_warnings"]') : null;
    var selected = checked("answer_id");
    var selectedEntry = scored.find(function (e) { return e.input === selected; });
    var needsConfirm = !!(windows && wimKnown && selectedEntry && selectedEntry.compat.status === "warning");

    if (warnBox && warnList && warnCheck) {
      warnBox.hidden = !needsConfirm;
      warnCheck.disabled = !needsConfirm;
      if (needsConfirm) {
        warnList.textContent = "";
        selectedEntry.compat.warnings.forEach(function (msg) {
          var li = document.createElement("li");
          li.textContent = msg;
          warnList.appendChild(li);
        });
      } else {
        warnCheck.checked = false;
      }
    }

    return needsConfirm && warnCheck && !warnCheck.checked;
  }

  function update(e) {
    if (e && e.target && e.target.type === "radio" && title.value.trim() === "") {
      titleEdited = false;
    }
    if (e && e.target && e.target.name === "package") {
      form.querySelectorAll('input[name="inf"][data-package="' + e.target.value + '"]').forEach(function (inf) {
        inf.checked = e.target.checked;
      });
    }
    syncPackages();
    var profile = checked("boot_profile_id");
    var kind = profile ? profile.dataset.kind : "";
    var windows = kind === "windows_iso";
    var winpe = windows || kind === "winpe_iso";
    var isUbuntu = kind === "ubuntu_builtin" || kind === "ubuntu_iso";

    ["windows-drivers", "windows-answers"].forEach(function (id) {
      var group = document.getElementById(id);
      if (!group) return;
      var available = id === "windows-drivers" ? winpe : windows;
      group.hidden = !available;
      group.disabled = !available;
    });

    var ubuntu = document.getElementById("ubuntu-autoinstall");
    if (ubuntu) {
      ubuntu.hidden = !isUbuntu;
      ubuntu.disabled = !isUbuntu;
    }
    ["summary-build-label", "summary-build"].forEach(function (id) {
      var el = document.getElementById(id);
      if (el) el.hidden = !winpe;
    });

    var blockedByUnconfirmedWarning = updateWindowsCompatibility(windows, profile);
    updateStepVisibility("boot-profiles", "boot-profile-search");
    updateStepVisibility("windows-drivers", "windows-driver-search");
    updateStepVisibility("windows-answers", "windows-answer-search");
    updateStepVisibility("ubuntu-autoinstall", "ubuntu-answer-search");

    text("menu-step-no", windows ? "4" : winpe ? "3" : isUbuntu ? "3" : "2");
    var answer = windows ? checked("answer_id") : null;
    var autoinstall = isUbuntu ? checked("autoinstall_id") : null;
    var scriptTitle = "";
    if (answer && answer.value !== "0") {
      scriptTitle = answer.dataset.title || "";
    } else if (autoinstall && autoinstall.value !== "0") {
      scriptTitle = autoinstall.dataset.title || "";
    }
    var osTitle = profile ? (profile.dataset.title || "") : "";

    if (!titleEdited) {
      title.value = composeMenuTitle(osTitle, scriptTitle);
    }
    var titleValid = applyFieldValidation(title, titleError, false);
    var folderValid = applyFieldValidation(folder, folderError, true);
    var hasInvalidMenuFields = !titleValid || !folderValid;
    var drivers = winpe && form.querySelectorAll('input[name="package"]:checked, input[name="inf"]:checked').length > 0;
    var wipes = (answer && answer.dataset.wipes === "true") || (autoinstall && autoinstall.dataset.wipes === "true");
    text("preview-title", (title.value || "Windows 11 x64"));
    text("preview-folder", folder.value ? " / " + folder.value.split("/").join(" / ") : "");
    text("preview-hint", wipes ? "!! WIPES DISK" : (drivers ? (en ? "drivers" : "драйверы") : ""));
    var hintEl = document.getElementById("preview-hint");
    if (hintEl) hintEl.classList.toggle("danger", !!wipes);
    text("summary-build", (drivers || profile && profile.dataset.prepare === "true") ? (en ? "needed, a few minutes" : "нужна, несколько минут") : (en ? "not needed" : "не нужна"));

    if (submitBtn && !submitBtn.dataset.submitting) {
      submitBtn.disabled = blockedByUnconfirmedWarning || hasInvalidMenuFields;
      submitBtn.textContent = defaultSubmitLabel;
    }
  }

  form.addEventListener("click", function (e) {
    var moreBtn = e.target.closest("[data-step-more]");
    if (!moreBtn) return;
    e.preventDefault();
    var stepKey = moreBtn.dataset.stepMore;
    expandedSteps[stepKey] = !expandedSteps[stepKey];
    update();
  });

  form.addEventListener("change", update);
  form.addEventListener("input", update);
  form.addEventListener("submit", function (e) {
    var titleValid = applyFieldValidation(title, titleError, false);
    var folderValid = applyFieldValidation(folder, folderError, true);
    if (!titleValid || !folderValid) {
      e.preventDefault();
      if (!titleValid) title.focus(); else folder.focus();
      return;
    }
    if (submitBtn) {
      submitBtn.dataset.submitting = "1";
      submitBtn.disabled = true;
      submitBtn.textContent = en ? "Preparing…" : "Подготавливается…";
    }
  });
  update();
})();
