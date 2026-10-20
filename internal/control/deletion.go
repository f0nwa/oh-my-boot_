package control

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

func deletionID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный номер записи", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (s *Server) activeMenuContains(r *http.Request, key string) (bool, error) {
	reader := s.DeletionMenu
	if reader == nil {
		reader = s.Boot
	}
	if reader == nil {
		return false, fmt.Errorf("boot menu unavailable")
	}
	live, err := reader.CurrentMenu(r.Context())
	if err != nil {
		return false, fmt.Errorf("boot menu unavailable: %w", err)
	}
	if len(live) == 0 || len(live) > 256<<10 {
		return false, fmt.Errorf("boot menu is empty or too large")
	}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		return false, err
	}
	for _, version := range versions {
		if version.Active {
			sum := sha256.Sum256(live)
			if hex.EncodeToString(sum[:]) != version.SHA256 || string(live) != version.Script {
				return false, fmt.Errorf("boot menu differs from active database version")
			}
			for _, line := range strings.Split(version.Script, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[0] == "item" && fields[1] == key {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, fmt.Errorf("active menu version unavailable")
}

func (s *Server) driverConfigDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := deletionID(w, r)
	if !ok {
		return
	}
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.Cleanup == nil {
		http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
		return
	}
	config, err := s.Store.DriverConfig(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Конфигурация не найдена", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать конфигурацию", http.StatusServiceUnavailable)
		return
	}
	active, err := s.activeMenuContains(r, fmt.Sprintf("windriver_%d", id))
	if err != nil {
		http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
		return
	}
	if active {
		s.rejectDeletion(w, r, "driver-config.delete", id, "published", "Сначала снимите пункт с публикации меню")
		return
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить общие файлы", http.StatusServiceUnavailable)
		return
	}
	wimKey := config.Build.WIMKey
	if wimKey == "" {
		wimKey, err = s.Cleanup.DriverConfigWIMKey(r.Context(), id)
		if err != nil {
			http.Error(w, "Не удалось проверить подготовленные файлы", http.StatusServiceUnavailable)
			return
		}
	}
	removeWIM := wimKey != ""
	for _, other := range configs {
		if other.ID != id && wimKey != "" && (other.Build.WIMKey == wimKey || (other.Build.WIMKey == "" && reflect.DeepEqual(other.PackageINFs, config.PackageINFs))) {
			removeWIM = false
			break
		}
	}
	if err := s.Cleanup.RemoveDriverConfig(r.Context(), id, wimKey, removeWIM); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-config.delete", "error", fmt.Sprintf("id=%d cleanup", id))
		http.Error(w, "Не удалось удалить подготовленные файлы", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.DeleteDriverConfig(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-config.delete", "error", fmt.Sprintf("id=%d database", id))
		http.Error(w, "Не удалось удалить запись", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-config.delete", "ok", fmt.Sprintf("id=%d", id))
	http.Redirect(w, r, "/drivers?message=Конфигурация+удалена", http.StatusSeeOther)
}

func (s *Server) windowsAnswerDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := deletionID(w, r)
	if !ok {
		return
	}
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.Cleanup == nil {
		http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
		return
	}
	_, err := s.Store.WindowsAnswer(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Сценарий не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать сценарий", http.StatusServiceUnavailable)
		return
	}
	active, err := s.activeMenuContains(r, fmt.Sprintf("winanswer_%d", id))
	if err != nil {
		http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
		return
	}
	if active {
		s.rejectDeletion(w, r, "windows-answer.delete", id, "published", "Сначала снимите пункт с публикации меню")
		return
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить зависимости", http.StatusServiceUnavailable)
		return
	}
	for _, config := range configs {
		if config.AnswerID == id {
			s.rejectDeletion(w, r, "windows-answer.delete", id, fmt.Sprintf("used by driver config %d", config.ID), fmt.Sprintf("Сначала удалите конфигурацию драйверов № %d", config.ID))
			return
		}
	}
	if err := s.Cleanup.RemoveWindowsAnswer(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.delete", "error", fmt.Sprintf("id=%d cleanup", id))
		http.Error(w, "Не удалось удалить подготовленные файлы", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.DeleteWindowsAnswer(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.delete", "error", fmt.Sprintf("id=%d database", id))
		http.Error(w, "Не удалось удалить запись", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.delete", "ok", fmt.Sprintf("id=%d", id))
	http.Redirect(w, r, "/images?message=Сценарий+удалён", http.StatusSeeOther)
}

func (s *Server) windowsProfileDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := deletionID(w, r)
	if !ok {
		return
	}
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.Cleanup == nil {
		http.Error(w, "Очистка файлов недоступна", http.StatusServiceUnavailable)
		return
	}
	_, err := s.Store.WindowsProfile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Профиль не найден", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать профиль", http.StatusServiceUnavailable)
		return
	}
	active, err := s.activeMenuContains(r, fmt.Sprintf("winiso_%d", id))
	if err != nil {
		http.Error(w, "Не удалось проверить меню", http.StatusServiceUnavailable)
		return
	}
	if active {
		s.rejectDeletion(w, r, "windows-profile.delete", id, "published", "Сначала снимите пункт с публикации меню")
		return
	}
	answers, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить сценарии", http.StatusServiceUnavailable)
		return
	}
	for _, answer := range answers {
		if answer.ProfileID == id {
			s.rejectDeletion(w, r, "windows-profile.delete", id, fmt.Sprintf("used by answer %d", answer.ID), fmt.Sprintf("Сначала удалите XML-сценарий № %d", answer.ID))
			return
		}
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Не удалось проверить конфигурации", http.StatusServiceUnavailable)
		return
	}
	for _, config := range configs {
		if config.ProfileID == id {
			s.rejectDeletion(w, r, "windows-profile.delete", id, fmt.Sprintf("used by driver config %d", config.ID), fmt.Sprintf("Сначала удалите конфигурацию драйверов № %d", config.ID))
			return
		}
	}
	if err := s.Cleanup.RemoveWindowsProfile(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-profile.delete", "error", fmt.Sprintf("id=%d cleanup", id))
		http.Error(w, "Не удалось удалить подготовленные файлы", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.DeleteWindowsProfile(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-profile.delete", "error", fmt.Sprintf("id=%d database", id))
		http.Error(w, "Не удалось удалить запись", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-profile.delete", "ok", fmt.Sprintf("id=%d", id))
	http.Redirect(w, r, "/images?message=Профиль+удалён", http.StatusSeeOther)
}

// rejectDeletion records a refused deletion in the audit log and reports it.
// Refused requests change nothing, but administrators can see the attempts.
func (s *Server) rejectDeletion(w http.ResponseWriter, r *http.Request, action string, id int64, reason, message string) {
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, "rejected", fmt.Sprintf("id=%d %s", id, reason))
	http.Error(w, message, http.StatusConflict)
}
