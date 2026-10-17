package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"pxe-platform/internal/agent"
)

func (s *Server) menuPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), MenuTitle: "PXE Platform", MenuLabel: "Diagnostics", MenuMessage: "PXE Platform network boot is ready"}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать версии меню", 500)
		return
	}
	data.Versions = versions
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать профили Windows ISO", 500)
		return
	}
	answers, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать сценарии Windows", 500)
		return
	}
	activeWindows := map[int64]bool{}
	activeAnswers := map[int64]menuWindowsAnswerRef{}
	activeFolders := map[string]string{}
	for _, version := range versions {
		if version.Active {
			data.MenuTitle = version.Title
			data.MenuLabel = version.Label
			data.MenuMessage = version.Message
			data.MenuUbuntu = strings.Contains(version.Script, "item ubuntu Ubuntu Desktop 26.04.1\n")
			data.MenuWindows10 = strings.Contains(version.Script, "item windows10 Windows 10 installer - WinPE\n")
			data.MenuWindows11 = strings.Contains(version.Script, "item windows11 Windows 11 installer - WinPE\n")
			folders := menuFolderPaths(version.Script)
			activeWindows = menuWindowsProfileIDs(version.Script)
			activeAnswers = menuWindowsAnswerRefs(version.Script)
			activeFolders = folders
			data.MenuFolderUbuntu = folders["ubuntu"]
			data.MenuFolderWindows10 = folders["windows10"]
			data.MenuFolderWindows11 = folders["windows11"]
			data.HasMenuFolders = data.MenuFolderUbuntu != "" || data.MenuFolderWindows10 != "" || data.MenuFolderWindows11 != ""
			break
		}
	}
	for _, profile := range profiles {
		key := fmt.Sprintf("winiso_%d", profile.ID)
		folder := profile.Folder
		if activeWindows[profile.ID] {
			folder = activeFolders[key]
		}
		data.MenuWindowsProfiles = append(data.MenuWindowsProfiles, menuWindowsProfileRow{WindowsProfile: profile, Selected: activeWindows[profile.ID], MenuFolder: folder})
	}
	for _, answer := range answers {
		key := fmt.Sprintf("winanswer_%d", answer.ID)
		folder := answer.Folder
		if _, ok := activeAnswers[answer.ID]; ok {
			folder = activeFolders[key]
		}
		data.MenuWindowsAnswers = append(data.MenuWindowsAnswers, menuWindowsAnswerRow{WindowsAnswerVariant: answer, Selected: activeAnswers[answer.ID].ID == answer.ID, MenuFolder: folder})
	}
	render(w, "menu", data)
}

func (s *Server) menuPublish(w http.ResponseWriter, r *http.Request) {
	title, label, message := r.FormValue("title"), r.FormValue("label"), r.FormValue("message")
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать профили Windows ISO", 503)
		return
	}
	answers, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать сценарии Windows", 503)
		return
	}
	script, err := BuildMenuFromFormWithProfilesAndAnswers(r.Form, s.BootBaseURL, profiles, answers)
	if err != nil {
		http.Error(w, "Недопустимый текст меню: "+err.Error(), 400)
		return
	}
	m := MenuVersion{Title: title, Label: label, Message: message, Script: string(script)}
	s.publishMenu(w, r, m, "menu.publish")
}

func (s *Server) menuRollback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверная версия", 400)
		return
	}
	m, err := s.Store.MenuByID(r.Context(), id)
	if err != nil {
		http.Error(w, "Версия не найдена", 404)
		return
	}
	if len(m.Script) == 0 || len(m.Script) > 256<<10 {
		http.Error(w, "Недопустимый снимок меню", 400)
		return
	}
	s.publishMenu(w, r, m, "menu.rollback")
}

func (s *Server) publishMenu(w http.ResponseWriter, r *http.Request, m MenuVersion, action string) {
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	if err := s.verifyMenuWindowsProfiles(r.Context(), m.Script); err != nil {
		http.Error(w, "Профиль Windows ISO недоступен: "+err.Error(), http.StatusConflict)
		return
	}
	info := requestInfo(r)
	if s.Boot == nil {
		http.Error(w, "Boot недоступен", 503)
		return
	}
	previous, err := s.Boot.CurrentMenu(r.Context())
	if err != nil {
		http.Error(w, "Не удалось получить текущее меню", 503)
		return
	}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать версии меню", 503)
		return
	}
	if len(versions) == 0 {
		oldSum := sha256.Sum256(previous)
		initial := MenuVersion{Title: "PXE Platform", Label: "Diagnostics", Message: "PXE Platform network boot is ready", Script: string(previous), SHA256: hex.EncodeToString(oldSum[:])}
		if err := s.Store.SaveActiveMenu(r.Context(), 0, initial); err != nil {
			http.Error(w, "Не удалось сохранить начальное меню", 503)
			return
		}
	}
	sum := sha256.Sum256([]byte(m.Script))
	m.SHA256 = hex.EncodeToString(sum[:])
	hash, err := s.Boot.Publish(r.Context(), []byte(m.Script))
	if err != nil {
		state, checkErr := s.Boot.Status(r.Context())
		if checkErr != nil || state.SHA256 != m.SHA256 {
			_ = s.Store.Audit(r.Context(), info.User.ID, action, "error", "boot publish")
			http.Error(w, "Публикация не подтверждена", 503)
			return
		}
		hash = state.SHA256
	}
	if hash != m.SHA256 {
		http.Error(w, "Контрольная сумма Boot не совпала", 502)
		return
	}
	if err := s.Store.SaveActiveMenu(r.Context(), info.User.ID, m); err != nil {
		_, rollbackErr := s.Boot.Publish(r.Context(), previous)
		_ = s.Store.Audit(r.Context(), info.User.ID, action, "error", "database write")
		if rollbackErr != nil {
			http.Error(w, "Не удалось сохранить версию и вернуть старое меню: "+fmt.Sprint(rollbackErr), 500)
			return
		}
		http.Error(w, "Не удалось сохранить версию; старое меню восстановлено", 500)
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, action, "ok", "sha256="+m.SHA256)
	http.Redirect(w, r, "/menu?message=Меню+опубликовано", 303)
}

func (s *Server) verifyMenuWindowsProfiles(ctx context.Context, script string) error {
	selected := menuWindowsProfileIDs(script)
	answerRefs := menuWindowsAnswerRefs(script)
	answerItems := map[int64]bool{}
	for _, line := range strings.Split(script, "\n") {
		if !strings.HasPrefix(line, "item winanswer_") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return fmt.Errorf("пункт сценария повреждён")
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(fields[1], "winanswer_"), 10, 64)
		if err != nil || id <= 0 || answerItems[id] {
			return fmt.Errorf("пункт сценария повреждён")
		}
		answerItems[id] = true
	}
	if len(answerItems) != len(answerRefs) {
		return fmt.Errorf("ссылки на сценарии повреждены")
	}
	for id := range answerItems {
		if _, ok := answerRefs[id]; !ok {
			return fmt.Errorf("ссылки на сценарии повреждены")
		}
	}
	for _, ref := range answerRefs {
		selected[ref.ProfileID] = true
	}
	if len(selected) == 0 {
		return nil
	}
	if s.Images == nil || s.WindowsISO == nil {
		return fmt.Errorf("службы образов недоступны")
	}
	profiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return err
	}
	if len(answerRefs) > 0 {
		if s.WindowsAnswers == nil {
			return fmt.Errorf("служба сценариев недоступна")
		}
		answers, err := s.Store.WindowsAnswers(ctx)
		if err != nil {
			return err
		}
		for _, answer := range answers {
			ref, ok := answerRefs[answer.ID]
			if !ok {
				continue
			}
			if answer.State != "ready" || answer.ProfileID != ref.ProfileID || answer.SHA256 != ref.SHA256 || answer.Size <= 0 {
				return fmt.Errorf("сценарий %d изменился или не подготовлен", answer.ID)
			}
			if err := s.WindowsAnswers.CheckWindowsAnswer(ctx, agent.WindowsAnswerCheck{ID: answer.ID, ProfileID: answer.ProfileID, SHA256: answer.SHA256}); err != nil {
				return fmt.Errorf("файлы сценария %d недоступны", answer.ID)
			}
			delete(answerRefs, answer.ID)
		}
		if len(answerRefs) != 0 {
			return fmt.Errorf("сценарий отсутствует")
		}
	}
	catalog, err := s.Images.Catalog(ctx)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if !selected[p.ID] {
			continue
		}
		if p.State != "ready" || (p.InstallFile != "install.wim" && p.InstallFile != "install.esd") {
			return fmt.Errorf("профиль %d не подготовлен", p.ID)
		}
		current, err := SelectWindowsISO(catalog, p.StorageID, p.Path, p.Title, p.Folder)
		if err != nil || current.StorageRoot != p.StorageRoot || current.Size != p.Size || !current.Modified.Equal(p.Modified) {
			return fmt.Errorf("исходный ISO профиля %d изменился", p.ID)
		}
		if err := s.WindowsISO.CheckWindowsISO(ctx, agent.WindowsISORequest{ID: p.ID, StorageRoot: p.StorageRoot, RelativePath: p.Path, Size: p.Size, Modified: p.Modified}); err != nil {
			return fmt.Errorf("файлы профиля %d недоступны", p.ID)
		}
		delete(selected, p.ID)
	}
	if len(selected) != 0 {
		return fmt.Errorf("профиль отсутствует")
	}
	return nil
}
