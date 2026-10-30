package control

import (
	"context"
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

type bootProfileChoice struct {
	BootProfile
	State, Error, Path string
}

func bootProfileChoices(shared []BootProfile, windows []WindowsProfile) []bootProfileChoice {
	choices := make([]bootProfileChoice, 0, len(shared))
	for _, profile := range shared {
		choice := bootProfileChoice{BootProfile: profile, State: "unavailable", Error: "Профиль загрузки недоступен"}
		switch profile.Kind {
		case BootProfileUbuntuBuiltin:
			choice.State, choice.Error, choice.Path = "ready", "", "Системный источник Ubuntu"
		case BootProfileWindowsISO:
			for _, source := range windows {
				if source.ID == profile.WindowsProfileID {
					choice.State, choice.Error, choice.Path = source.State, source.Error, source.Path
					if !profileBootable(source) && choice.State == "ready" {
						choice.State = "unavailable"
						if choice.Error == "" {
							choice.Error = "Профиль загрузки недоступен"
						}
					}
				}
			}
		}
		choices = append(choices, choice)
	}
	return choices
}

func (s *Server) installationNewPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Error: pageError(r)}
	profiles, answers, _, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Каталог драйверов недоступен", http.StatusInternalServerError)
		return
	}
	data.BootProfileChoices = bootProfileChoices(sources.BootProfiles, profiles)
	data.WindowsAnswerVariants, data.PackageChoices = answers, packageChoices(packages)
	data.Form = pageForm(r)
	if data.Form == nil {
		data.Form = url.Values{}
		selected := r.URL.Query().Get("boot_profile_id")
		for _, profile := range data.BootProfileChoices {
			if (selected != "" && selected == strconv.FormatInt(profile.ID, 10)) || (selected == "" && profile.State == "ready") {
				data.Form.Set("boot_profile_id", strconv.FormatInt(profile.ID, 10))
				if profile.Kind == BootProfileWindowsISO {
					data.Form.Set("folder", "Windows")
				} else {
					data.Form.Set("folder", "Linux")
				}
				break
			}
		}
	}
	for _, profile := range data.BootProfileChoices {
		if data.Form.Get("boot_profile_id") == strconv.FormatInt(profile.ID, 10) {
			data.SelectedBootKind = profile.Kind
		}
	}
	render(w, "installation_new", data)
}

// installationCreate saves an independent installation and adds it to the draft.
func (s *Server) installationCreate(w http.ResponseWriter, r *http.Request) {
	s.draftMu.Lock()
	defer s.draftMu.Unlock()
	profiles, answers, _, err := s.menuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	sources, err := s.installationMenuSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	creator, ok := s.Store.(interface {
		CreateInstallation(context.Context, Installation) (Installation, error)
	})
	if !ok {
		http.Error(w, "Не удалось создать установку", http.StatusServiceUnavailable)
		return
	}
	profileID, err := strconv.ParseInt(r.FormValue("boot_profile_id"), 10, 64)
	var shared BootProfile
	for _, choice := range bootProfileChoices(sources.BootProfiles, profiles) {
		if choice.ID == profileID && choice.State == "ready" {
			shared = choice.BootProfile
		}
	}
	if err != nil || shared.ID == 0 {
		http.Error(w, "Выберите подготовленный профиль загрузки", http.StatusBadRequest)
		return
	}
	var profile WindowsProfile
	for _, p := range profiles {
		if p.ID == shared.WindowsProfileID {
			profile = p
		}
	}
	if shared.Kind == BootProfileUbuntuBuiltin && ((r.FormValue("answer_id") != "" && r.FormValue("answer_id") != "0") || len(r.Form["package"]) > 0 || len(r.Form["inf"]) > 0) {
		http.Error(w, "Ubuntu не поддерживает компоненты Windows", http.StatusBadRequest)
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
		title = shared.Title
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
	item := Installation{BootProfileID: shared.ID, Title: title, Folder: folder, AnswerID: answerID}
	if len(infs) > 0 {
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
			if sameDriverSelection(other, config) && (other.State == "pending" || other.State == "preparing" || other.State == "ready") {
				if other.State == "ready" {
					if _, _, err := menuProfileForKey(fmt.Sprintf("windriver_%d", other.ID), "", profiles, answers, existing); err != nil {
						continue
					}
				}
				config = other
				break
			}
		}
		if config.ID == 0 {
			config, err = s.Store.CreateDriverConfig(r.Context(), config)
			if err != nil {
				http.Error(w, "Не удалось создать конфигурацию", http.StatusServiceUnavailable)
				return
			}
		}
		item.AnswerID, item.DriverConfigID = 0, config.ID
	}
	item, err = creator.CreateInstallation(r.Context(), item)
	if err != nil {
		http.Error(w, "Не удалось создать установку", http.StatusServiceUnavailable)
		return
	}
	if err := draft.AddInstallation(item); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
		http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "installation.create", "ok", fmt.Sprintf("install_%d", item.ID))
	http.Redirect(w, r, "/installations?message=Установка+создана", http.StatusSeeOther)
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
