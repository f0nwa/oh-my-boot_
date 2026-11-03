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
	"time"
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
	Value, StorageName string
	DefaultTitle       string
}

// bootProfileChoices checks the shared Ubuntu resources once per request.
func (s *Server) bootProfileChoices(ctx context.Context, shared []BootProfile, windows []WindowsProfile) []bootProfileChoice {
	var ubuntuProblem error
	ubuntuChecked := false
	choices := make([]bootProfileChoice, 0, len(shared))
	for _, profile := range shared {
		choice := bootProfileChoice{BootProfile: profile, State: "unavailable", Error: "Профиль загрузки недоступен"}
		switch profile.Kind {
		case BootProfileUbuntuBuiltin:
			choice.State, choice.Error, choice.Path = "ready", "", "Системный источник Ubuntu"
			if !ubuntuChecked {
				ubuntuProblem = s.checkUbuntuReadiness(ctx)
				ubuntuChecked = true
			}
			if ubuntuProblem != nil {
				choice.State, choice.Error = "unavailable", ubuntuProblem.Error()
			}
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
	data.BootProfileChoices, err = s.downloadSourceChoices(r.Context(), sources.BootProfiles, profiles, r.PathValue("id") == "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	data.WindowsAnswerVariants, data.PackageChoices = answers, packageChoices(packages)
	data.UbuntuAutoinstalls = sources.Autoinstalls
	if raw := r.PathValue("id"); raw != "" {
		data.EditInstallationID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || data.EditInstallationID <= 0 {
			http.Error(w, "Неверная загрузка", http.StatusBadRequest)
			return
		}
	}
	data.Form = pageForm(r)
	if data.EditInstallationID > 0 && data.Form == nil {
		found := false
		for _, item := range sources.Installations {
			if item.ID == data.EditInstallationID {
				found = true
				data.Form = url.Values{"boot_profile_id": {strconv.FormatInt(item.BootProfileID, 10)}, "title": {item.Title}, "folder": {item.Folder}, "answer_id": {strconv.FormatInt(item.AnswerID, 10)}, "autoinstall_id": {strconv.FormatInt(item.AutoinstallID, 10)}}
				if item.DriverConfigID > 0 {
					configs, err := s.Store.DriverConfigs(r.Context())
					if err != nil {
						http.Error(w, "Не удалось прочитать конфигурации драйверов", http.StatusServiceUnavailable)
						return
					}
					for _, config := range configs {
						if config.ID == item.DriverConfigID {
							data.Form.Set("answer_id", strconv.FormatInt(config.AnswerID, 10))
							for _, inf := range config.PackageINFs {
								data.Form.Add("inf", inf.PackageSHA256+":"+inf.INFPath)
							}
						}
					}
				}
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	if data.Form == nil {
		data.Form = url.Values{}
		selected := r.URL.Query().Get("boot_profile_id")
		if sid := r.URL.Query().Get("storage_id"); sid != "" {
			selected = "iso:" + sid + ":" + r.URL.Query().Get("path")
			for _, p := range profiles {
				if strconv.FormatInt(p.StorageID, 10) == sid && p.Path == r.URL.Query().Get("path") {
					for _, c := range data.BootProfileChoices {
						if c.WindowsProfileID == p.ID {
							selected = c.Value
						}
					}
				}
			}
		}
		for _, profile := range data.BootProfileChoices {
			if (selected != "" && selected == profile.Value) || (selected == "" && profile.Selectable()) {
				data.Form.Set("boot_profile_id", profile.Value)
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
		if data.Form.Get("boot_profile_id") == profile.Value {
			data.SelectedBootKind = profile.Kind
		}
	}
	render(w, "installation_new", data)
}

// installationCreate saves an independent installation and adds it to the draft.
func (s *Server) installationCreate(w http.ResponseWriter, r *http.Request) {
	s.draftMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.draftMu.Unlock()
		}
	}()
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
	shared, profile, newSource, err := s.resolveDownloadSource(r, sources, profiles)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	editID := int64(0)
	if raw := r.PathValue("id"); raw != "" {
		editID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || editID <= 0 {
			http.Error(w, "Неверная загрузка", http.StatusBadRequest)
			return
		}
	}
	if editID > 0 {
		found := false
		for _, item := range sources.Installations {
			if item.ID == editID {
				found = true
				if newSource != nil || item.BootProfileID != shared.ID {
					http.Error(w, "Источник существующей загрузки нельзя заменить", http.StatusBadRequest)
					return
				}
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	if shared.Kind == BootProfileUbuntuBuiltin && ((r.FormValue("answer_id") != "" && r.FormValue("answer_id") != "0") || len(r.Form["package"]) > 0 || len(r.Form["inf"]) > 0) {
		http.Error(w, "Ubuntu не поддерживает компоненты Windows", http.StatusBadRequest)
		return
	}
	autoinstallID := int64(0)
	if raw := r.FormValue("autoinstall_id"); raw != "" && raw != "0" {
		autoinstallID, err = strconv.ParseInt(raw, 10, 64)
		found := false
		for _, file := range sources.Autoinstalls {
			if file.ID == autoinstallID && file.State == "validated" {
				found = true
			}
		}
		if err != nil || autoinstallID <= 0 || shared.Kind != BootProfileUbuntuBuiltin || !found {
			http.Error(w, "Выберите проверенный файл Autoinstall только для Ubuntu", http.StatusBadRequest)
			return
		}
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
	var selectedConfig *DriverConfig
	item := Installation{BootProfileID: shared.ID, Title: title, Folder: folder, AnswerID: answerID, AutoinstallID: autoinstallID}
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
		selectedINFs, err := selectDriverINFs(values["inf"], packages)
		config := DriverConfig{ProfileID: profile.ID, AnswerID: answerID, Title: title, Folder: folder, PackageINFs: selectedINFs, State: "pending"}
		if err != nil {
			http.Error(w, "Неверная конфигурация: "+err.Error(), http.StatusBadRequest)
			return
		}
		item.AnswerID = 0
		selectedConfig = &config
	}
	if newSource != nil {
		profile, err = s.Store.CreateWindowsProfile(r.Context(), *newSource)
		if err != nil {
			http.Error(w, "Не удалось сохранить источник ISO", http.StatusServiceUnavailable)
			return
		}
		bootProfiles, err := s.Store.BootProfiles(r.Context())
		if err != nil {
			http.Error(w, "Источник загрузки недоступен", http.StatusServiceUnavailable)
			return
		}
		for _, p := range bootProfiles {
			if p.Kind == BootProfileWindowsISO && p.WindowsProfileID == profile.ID {
				shared = p
				item.BootProfileID = p.ID
			}
		}
		if item.BootProfileID == 0 {
			http.Error(w, "Источник загрузки недоступен", http.StatusServiceUnavailable)
			return
		}
	}
	if selectedConfig != nil {
		selectedConfig.ProfileID = profile.ID
		existing, err := s.Store.DriverConfigs(r.Context())
		if err != nil {
			http.Error(w, "Не удалось прочитать конфигурации драйверов", http.StatusServiceUnavailable)
			return
		}
		for _, other := range existing {
			if sameDriverSelection(other, *selectedConfig) {
				if other.State == "ready" {
					if _, _, e := windowsMenuProfile(Installation{DriverConfigID: other.ID}, other.ProfileID, "", profiles, answers, existing); e != nil {
						continue
					}
				}
				*selectedConfig = other
				break
			}
		}
		if selectedConfig.ID == 0 {
			created, e := s.Store.CreateDriverConfig(r.Context(), *selectedConfig)
			if e != nil {
				http.Error(w, "Не удалось создать конфигурацию", http.StatusServiceUnavailable)
				return
			}
			*selectedConfig = created
		}
		item.DriverConfigID = selectedConfig.ID
	}

	if editID > 0 {
		item.ID = editID
		err = s.Store.UpdateInstallation(r.Context(), item)
	} else {
		item, err = s.Store.CreateInstallation(r.Context(), item)
	}
	if err != nil {
		http.Error(w, "Не удалось сохранить загрузку", http.StatusServiceUnavailable)
		return
	}
	if editID > 0 && draft.InstallationIndex(editID) >= 0 {
		err = draft.UpdateInstallation(editID, title, folder)
	} else if editID == 0 {
		err = draft.AddInstallation(item)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if i := draft.InstallationIndex(item.ID); i >= 0 {
		draft.Items[i].Components = installationComponents(item)
	}
	if err := s.Store.SaveMenuDraft(r.Context(), draft); err != nil {
		http.Error(w, "Не удалось сохранить черновик меню", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "installation.create", "ok", fmt.Sprintf("install_%d", item.ID))
	// The menu lock is released before slow systemd/WIM work; a saved failed
	// entry remains editable and retryable, and publishing still checks readiness.
	s.draftMu.Unlock()
	locked = false
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(22 * time.Minute))
	problem := s.prepareDownload(r.Context(), item, requestInfo(r).User.ID)
	message := "Загрузка создана"
	if editID > 0 {
		message = "Загрузка сохранена"
	}
	if problem != nil {
		message = "Загрузка сохранена. Подготовка не завершена; подробности в строке загрузки."
	}
	http.Redirect(w, r, "/installations?message="+url.QueryEscape(message), http.StatusSeeOther)
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
