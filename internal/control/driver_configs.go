package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pxe-platform/internal/agent"
)

type DriverConfig struct {
	ID          int64
	ProfileID   int64
	AnswerID    int64
	Title       string
	Folder      string
	PackageINFs []agent.SelectedINF
	State       string
	Build       agent.DriverBuildResult
	Error       string
}

func SelectDriverConfig(form url.Values, profiles []WindowsProfile, answers []WindowsAnswerVariant, packages []DriverPackage) (DriverConfig, error) {
	var result DriverConfig
	profileID, err := strconv.ParseInt(form.Get("profile_id"), 10, 64)
	if err != nil || profileID <= 0 {
		return result, fmt.Errorf("invalid Windows profile")
	}
	answerID := int64(0)
	if raw := form.Get("answer_id"); raw != "" && raw != "0" {
		answerID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || answerID <= 0 {
			return result, fmt.Errorf("invalid answer selection")
		}
	}
	title, folder := form.Get("title"), form.Get("folder")
	if !safeMenuText(title, 80) || !isASCII(title) || validateMenuFolder(folder) != nil || !isASCII(folder) {
		return result, fmt.Errorf("title and menu folder must be ASCII")
	}
	parentReady := false
	for _, profile := range profiles {
		if profile.ID == profileID && profile.State == "ready" && (profile.InstallFile == "install.wim" || profile.InstallFile == "install.esd") {
			parentReady = true
			break
		}
	}
	if !parentReady {
		return result, fmt.Errorf("Windows ISO profile is not ready")
	}
	if answerID > 0 {
		answerReady := false
		for _, answer := range answers {
			if answer.ID == answerID && answer.ProfileID == profileID && answer.State == "ready" && answer.Size > 0 && validAnswerHash(answer.SHA256) {
				answerReady = true
				break
			}
		}
		if !answerReady {
			return result, fmt.Errorf("answer scenario is not ready for this ISO")
		}
	}
	choices := form["inf"]
	if len(choices) == 0 || len(choices) > 128 {
		return result, fmt.Errorf("select 1–128 INF files")
	}
	seen := map[string]bool{}
	for _, choice := range choices {
		parts := strings.SplitN(choice, ":", 2)
		if len(parts) != 2 || !validAnswerHash(parts[0]) || parts[1] == "" || seen[choice] {
			return result, fmt.Errorf("invalid INF selection")
		}
		seen[choice] = true
		found := false
		for _, pkg := range packages {
			if pkg.SHA256 != parts[0] {
				continue
			}
			for _, inf := range pkg.Inventory.INFs {
				if inf.Path == parts[1] && inf.Architecture == "amd64" {
					found = true
					break
				}
			}
		}
		if !found {
			return result, fmt.Errorf("selected INF is unavailable or not x64")
		}
		result.PackageINFs = append(result.PackageINFs, agent.SelectedINF{PackageSHA256: parts[0], INFPath: parts[1]})
	}
	result.ProfileID, result.AnswerID, result.Title, result.Folder, result.State = profileID, answerID, title, folder, "pending"
	return result, nil
}

func (s *Server) driverConfigCreate(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Профили ISO недоступны", http.StatusServiceUnavailable)
		return
	}
	answers, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		http.Error(w, "Сценарии Windows недоступны", http.StatusServiceUnavailable)
		return
	}
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Каталог драйверов недоступен", http.StatusServiceUnavailable)
		return
	}
	config, err := SelectDriverConfig(r.Form, profiles, answers, packages)
	if err != nil {
		http.Error(w, "Неверная конфигурация: "+err.Error(), http.StatusBadRequest)
		return
	}
	config, err = s.Store.CreateDriverConfig(r.Context(), config)
	if err != nil {
		http.Error(w, "Не удалось создать конфигурацию", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-config.create", "ok", fmt.Sprintf("id=%d profile=%d", config.ID, config.ProfileID))
	http.Redirect(w, r, "/drivers?message=Конфигурация+создана", http.StatusSeeOther)
}

func (s *Server) driverConfigPrepare(w http.ResponseWriter, r *http.Request) {
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.DriverConfigs == nil {
		http.Error(w, "Подготовка конфигураций недоступна", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверная конфигурация", http.StatusBadRequest)
		return
	}
	config, err := s.Store.DriverConfig(r.Context(), id)
	if err != nil {
		http.Error(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}
	if config.State == "ready" {
		http.Redirect(w, r, "/drivers?message=Конфигурация+уже+готова", http.StatusSeeOther)
		return
	}
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Профили ISO недоступны", http.StatusServiceUnavailable)
		return
	}
	answers, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		http.Error(w, "Сценарии Windows недоступны", http.StatusServiceUnavailable)
		return
	}
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Каталог драйверов недоступен", http.StatusServiceUnavailable)
		return
	}
	values := url.Values{"profile_id": {strconv.FormatInt(config.ProfileID, 10)}, "answer_id": {strconv.FormatInt(config.AnswerID, 10)}, "title": {config.Title}, "folder": {config.Folder}}
	for _, inf := range config.PackageINFs {
		values.Add("inf", inf.PackageSHA256+":"+inf.INFPath)
	}
	if _, err := SelectDriverConfig(values, profiles, answers, packages); err != nil {
		http.Error(w, "Источник конфигурации недоступен: "+err.Error(), http.StatusConflict)
		return
	}
	if err := s.Store.SetDriverConfigResult(r.Context(), id, "preparing", agent.DriverBuildResult{}, ""); err != nil {
		http.Error(w, "Не удалось начать подготовку", http.StatusServiceUnavailable)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(16 * time.Minute))
	buildCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	result, err := s.DriverConfigs.PrepareDriverConfig(buildCtx, agent.DriverBuildRequest{ConfigID: id, ProfileID: config.ProfileID, AnswerID: config.AnswerID, PackageINFs: config.PackageINFs})
	finalCtx, cancelFinal := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancelFinal()
	if err != nil {
		_ = s.Store.SetDriverConfigResult(finalCtx, id, "error", agent.DriverBuildResult{}, "Не удалось подготовить WinPE")
		_ = s.Store.Audit(finalCtx, requestInfo(r).User.ID, "driver-config.prepare", "error", fmt.Sprintf("id=%d", id))
		http.Error(w, "Не удалось подготовить WinPE: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.SetDriverConfigResult(finalCtx, id, "ready", result, ""); err != nil {
		http.Error(w, "Не удалось сохранить результат", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(finalCtx, requestInfo(r).User.ID, "driver-config.prepare", "ok", fmt.Sprintf("id=%d wim=%s", id, result.WIMKey))
	http.Redirect(w, r, "/drivers?message=Конфигурация+подготовлена", http.StatusSeeOther)
}
