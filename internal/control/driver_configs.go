package control

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"omb/internal/agent"
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
	service := false
	for _, profile := range profiles {
		if profile.ID == profileID && profileBootable(profile) {
			parentReady = true
			service = profile.BootMode == agent.ISOStrelecMode
			break
		}
	}
	if !parentReady {
		return result, fmt.Errorf("Windows ISO profile is not ready")
	}
	if service && answerID != 0 {
		return result, fmt.Errorf("service WinPE does not support installer answers")
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
	selected, err := selectDriverINFs(form["inf"], packages)
	if err != nil {
		return result, err
	}
	result.PackageINFs = selected
	result.ProfileID, result.AnswerID, result.Title, result.Folder, result.State = profileID, answerID, title, folder, "pending"
	return result, nil
}

func (s *Server) driverConfigCreate(w http.ResponseWriter, r *http.Request) {
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
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

// selectDriverINFs validates a configuration before its source is prepared.
func selectDriverINFs(choices []string, packages []DriverPackage) ([]agent.SelectedINF, error) {
	var selected []agent.SelectedINF
	if len(choices) == 0 || len(choices) > 128 {
		return nil, fmt.Errorf("select 1–128 INF files")
	}
	seen := map[string]bool{}
	for _, choice := range choices {
		parts := strings.SplitN(choice, ":", 2)
		if len(parts) != 2 || !validAnswerHash(parts[0]) || parts[1] == "" || seen[choice] {
			return nil, fmt.Errorf("invalid INF selection")
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
			return nil, fmt.Errorf("selected INF is unavailable or not x64")
		}
		selected = append(selected, agent.SelectedINF{PackageSHA256: parts[0], INFPath: parts[1]})
	}
	return selected, nil
}
