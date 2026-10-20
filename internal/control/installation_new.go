package control

import (
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

//go:embed installation_form.js
var installationFormJS []byte

func installationFormScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(installationFormJS)
}

type infChoice struct{ Value, Label string }

// packageChoice is a driver package offered on the installation form, named
// by its x64 INF files because packages carry no name of their own.
type packageChoice struct {
	SHA256 string
	Label  string
	INFs   []infChoice
}

func packageChoices(packages []DriverPackage) []packageChoice {
	var out []packageChoice
	for _, pkg := range packages {
		choice := packageChoice{SHA256: pkg.SHA256}
		var names []string
		for _, inf := range pkg.Inventory.INFs {
			if inf.Architecture != "amd64" {
				continue
			}
			name := strings.TrimSuffix(strings.TrimSuffix(path.Base(inf.Path), ".inf"), ".INF")
			names = append(names, name)
			label := inf.Path
			if inf.Class != "" {
				label += " · " + inf.Class
			}
			choice.INFs = append(choice.INFs, infChoice{Value: pkg.SHA256 + ":" + inf.Path, Label: label})
		}
		if len(choice.INFs) == 0 {
			continue
		}
		choice.Label = strings.Join(names, ", ")
		out = append(out, choice)
	}
	return out
}

func (s *Server) installationNewPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Error: pageError(r)}
	profiles, answers, _, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Каталог драйверов недоступен", http.StatusInternalServerError)
		return
	}
	data.WindowsProfiles, data.WindowsAnswerVariants, data.PackageChoices = profiles, answers, packageChoices(packages)
	if form := pageForm(r); form != nil {
		data.Form = form
	}
	render(w, "installation_new", data)
}

// installationCreate turns the form into a menu draft item: a bare profile, a
// profile's answer file, or a new driver configuration that still needs a build.
func (s *Server) installationCreate(w http.ResponseWriter, r *http.Request) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	profiles, answers, _, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	profileID, err := strconv.ParseInt(r.FormValue("profile_id"), 10, 64)
	var profile WindowsProfile
	for _, p := range profiles {
		if p.ID == profileID {
			profile = p
		}
	}
	if err != nil || profile.ID == 0 || !profileBootable(profile) {
		http.Error(w, "Выберите подготовленный образ", http.StatusBadRequest)
		return
	}
	answerID, answerTitle := int64(0), ""
	if raw := r.FormValue("answer_id"); raw != "" && raw != "0" {
		answerID, err = strconv.ParseInt(raw, 10, 64)
		found := false
		for _, a := range answers {
			if a.ID == answerID {
				found, answerTitle = a.State == "ready", a.Title
				if a.ProfileID != profile.ID {
					http.Error(w, "Файл ответов относится к другому образу", http.StatusBadRequest)
					return
				}
			}
		}
		if err != nil || !found {
			http.Error(w, "Файл ответов не найден", http.StatusBadRequest)
			return
		}
	}
	title, folder := strings.TrimSpace(r.FormValue("title")), strings.TrimSpace(r.FormValue("folder"))
	if title == "" {
		title = profile.Title
		if answerTitle != "" {
			title = answerTitle
		}
	}
	if !safeMenuText(title, 80) || !isASCII(title) {
		http.Error(w, "Название в меню — только латиница, цифры и простые знаки, до 80 символов", http.StatusBadRequest)
		return
	}
	if validateMenuFolder(folder) != nil || !isASCII(folder) {
		http.Error(w, "Недопустимый каталог в меню", http.StatusBadRequest)
		return
	}
	draft, _, err := s.currentDraft(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать черновик меню", http.StatusServiceUnavailable)
		return
	}
	infs := r.Form["inf"]
	if selected := r.Form["package"]; len(selected) > 0 {
		packages, err := s.Store.DriverPackages(r.Context())
		if err != nil {
			http.Error(w, "Каталог драйверов недоступен", http.StatusServiceUnavailable)
			return
		}
		for _, sha := range selected {
			found := false
			for _, choice := range packageChoices(packages) {
				if choice.SHA256 == sha {
					found = true
					for _, inf := range choice.INFs {
						infs = append(infs, inf.Value)
					}
				}
			}
			if !found {
				http.Error(w, "Пакет драйверов не найден", http.StatusBadRequest)
				return
			}
		}
	}
	var key string
	if len(infs) == 0 {
		key = fmt.Sprintf("winiso_%d", profile.ID)
		if answerID > 0 {
			key = fmt.Sprintf("winanswer_%d", answerID)
		}
		if draftItemIndex(&draft, key) >= 0 {
			http.Error(w, "Такая установка уже в меню", http.StatusConflict)
			return
		}
	} else {
		unique := map[string]bool{}
		values := url.Values{"profile_id": {strconv.FormatInt(profile.ID, 10)}, "answer_id": {strconv.FormatInt(answerID, 10)}, "title": {title}, "folder": {folder}}
		for _, inf := range infs {
			if !unique[inf] {
				unique[inf] = true
				values.Add("inf", inf)
			}
		}
		packages, err := s.Store.DriverPackages(r.Context())
		if err != nil {
			http.Error(w, "Каталог драйверов недоступен", http.StatusServiceUnavailable)
			return
		}
		config, err := SelectDriverConfig(values, profiles, answers, packages)
		if err != nil {
			http.Error(w, "Неверная конфигурация: "+err.Error(), http.StatusBadRequest)
			return
		}
		existing, err := s.Store.DriverConfigs(r.Context())
		if err != nil {
			http.Error(w, "Не удалось прочитать конфигурации драйверов", http.StatusServiceUnavailable)
			return
		}
		for _, other := range existing {
			if sameDriverSelection(other, config) {
				http.Error(w, fmt.Sprintf("Такая установка уже есть: «%s» (windriver_%d)", other.Title, other.ID), http.StatusConflict)
				return
			}
		}
		config, err = s.Store.CreateDriverConfig(r.Context(), config)
		if err != nil {
			http.Error(w, "Не удалось создать конфигурацию", http.StatusServiceUnavailable)
			return
		}
		key = fmt.Sprintf("windriver_%d", config.ID)
	}
	draft.Items = append(draft.Items, MenuDraftItem{Key: key, Folder: folder, Title: title})
	if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
		http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "installation.create", "ok", key)
	http.Redirect(w, r, "/?message=Установка+создана", http.StatusSeeOther)
}

// sameDriverSelection reports whether two configurations boot the same image
// with the same answer file and the same INF files.
func sameDriverSelection(a, b DriverConfig) bool {
	if a.ProfileID != b.ProfileID || a.AnswerID != b.AnswerID || len(a.PackageINFs) != len(b.PackageINFs) {
		return false
	}
	set := map[string]bool{}
	for _, inf := range a.PackageINFs {
		set[inf.PackageSHA256+":"+inf.INFPath] = true
	}
	for _, inf := range b.PackageINFs {
		if !set[inf.PackageSHA256+":"+inf.INFPath] {
			return false
		}
	}
	return true
}
