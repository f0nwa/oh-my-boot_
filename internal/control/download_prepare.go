package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"omb/internal/agent"
)

// Preparation outlives browser disconnects. Its result belongs to the saved
// source/configuration, so every entry sharing those files sees the same state.
func (s *Server) prepareDownload(requestCtx context.Context, item Installation, actor int64) error {
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), 21*time.Minute)
	defer cancel()
	sources, err := s.Store.BootProfiles(ctx)
	if err != nil {
		return err
	}
	var sourceID int64
	for _, source := range sources {
		if source.ID == item.BootProfileID {
			if source.Kind == BootProfileUbuntuBuiltin {
				return s.checkUbuntuReadiness(ctx)
			}
			sourceID = source.WindowsProfileID
		}
	}
	if sourceID <= 0 {
		return fmt.Errorf("Источник загрузки недоступен")
	}
	profiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return err
	}
	var profile WindowsProfile
	for _, p := range profiles {
		if p.ID == sourceID {
			profile = p
		}
	}
	if profile.ID == 0 {
		return fmt.Errorf("Источник загрузки недоступен")
	}
	if err := s.prepareDownloadISO(ctx, profile, actor); err != nil {
		return err
	}
	if item.DriverConfigID == 0 {
		return nil
	}
	configs, err := s.Store.DriverConfigs(ctx)
	if err != nil {
		return err
	}
	for _, config := range configs {
		if config.ID == item.DriverConfigID {
			if config.State == "ready" {
				return nil
			}
			if err := s.Store.SetDriverConfigResult(ctx, config.ID, "preparing", agent.DriverBuildResult{}, ""); err != nil {
				return err
			}
			var result agent.DriverBuildResult
			var problem error
			if s.DriverConfigs == nil {
				problem = fmt.Errorf("Подготовка WinPE недоступна")
			} else {
				buildCtx, stop := context.WithTimeout(ctx, 15*time.Minute)
				result, problem = s.DriverConfigs.PrepareDriverConfig(buildCtx, agent.DriverBuildRequest{ConfigID: config.ID, ProfileID: profile.ID, AnswerID: config.AnswerID, PackageINFs: config.PackageINFs})
				stop()
			}
			finalCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer stop()
			if problem != nil {
				message := "Не удалось подготовить WinPE. Проверьте доступность Agent, пакеты драйверов и журнал omb-agent."
				_ = s.Store.SetDriverConfigResult(finalCtx, config.ID, "error", agent.DriverBuildResult{}, message)
				return fmt.Errorf("%s", message)
			}
			return s.Store.SetDriverConfigResult(finalCtx, config.ID, "ready", result, "")
		}
	}
	return fmt.Errorf("Конфигурация не найдена")
}

func (s *Server) prepareDownloadISO(ctx context.Context, profile WindowsProfile, actor int64) error {
	if s.Images != nil {
		catalog, err := s.Images.Catalog(ctx)
		if err != nil {
			return s.saveDownloadISOError(ctx, profile.ID, "unavailable", "Каталог ISO недоступен")
		}
		if problem := windowsProfileSourceProblem(catalog, profile); problem != nil {
			return s.saveDownloadISOError(ctx, profile.ID, "unavailable", capitalizeFirst(problem.Error()))
		}
	}
	request := agent.WindowsISORequest{ID: profile.ID, BootMode: profile.BootMode, StorageRoot: profile.StorageRoot, RelativePath: profile.Path, Size: profile.Size, Modified: profile.Modified}
	if profileBootable(profile) && (s.WindowsISO == nil || s.WindowsISO.CheckWindowsISO(ctx, request) == nil) {
		return nil
	}
	if s.Images == nil || s.WindowsISO == nil {
		return s.saveDownloadISOError(ctx, profile.ID, "error", "Подготовка Windows ISO недоступна")
	}
	if err := s.Store.SetWindowsProfileResult(ctx, profile.ID, "preparing", "", ""); err != nil {
		return err
	}
	prepCtx, stop := context.WithTimeout(ctx, 5*time.Minute)
	result, err := s.WindowsISO.PrepareWindowsISO(prepCtx, request)
	stop()
	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err != nil {
		message := downloadISOError(err)
		_ = s.Store.Audit(finalCtx, actor, "windows-profile.prepare", "error", fmt.Sprintf("id=%d", profile.ID))
		return s.saveDownloadISOError(finalCtx, profile.ID, "error", message)
	}
	if (profile.BootMode == agent.ISOStrelecMode && result.InstallFile != "") || (profile.BootMode == "" && result.InstallFile != "install.wim" && result.InstallFile != "install.esd") {
		return s.saveDownloadISOError(finalCtx, profile.ID, "error", "В ISO не найден установочный образ Windows")
	}
	if err := s.Store.SetWindowsProfileResult(finalCtx, profile.ID, "ready", result.InstallFile, ""); err != nil {
		return err
	}
	_ = s.Store.Audit(finalCtx, actor, "windows-profile.prepare", "ok", fmt.Sprintf("id=%d", profile.ID))
	return nil
}

func (s *Server) saveDownloadISOError(ctx context.Context, id int64, state, message string) error {
	if err := s.Store.SetWindowsProfileResult(ctx, id, state, "", message); err != nil {
		return fmt.Errorf("Не удалось сохранить результат подготовки")
	}
	return fmt.Errorf("%s", message)
}

func downloadISOError(err error) string {
	// Only allowlisted causes cross the Agent/UI boundary. Raw subprocess
	// errors can include host paths or credentials and must not reach the page.
	text := err.Error()
	switch {
	case strings.Contains(text, "not-windows-installer"):
		return "В ISO не найдены файлы установщика Windows. Для Ubuntu и сервисных WinPE нужен другой способ загрузки."
	case strings.Contains(text, "source-unavailable"):
		return "Исходный ISO изменился или недоступен. Проверьте хранилище и обновите каталог."
	case strings.Contains(text, "DeadlineExceeded") || strings.Contains(text, "deadline exceeded"):
		return "Подготовка ISO заняла больше пяти минут. Проверьте хранилище и журнал omb-agent."
	default:
		return "Не удалось подготовить ISO. Проверьте формат образа, доступность хранилища и журнал omb-agent."
	}
}

func (s *Server) installationPrepare(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверная загрузка", http.StatusBadRequest)
		return
	}
	items, err := s.Store.Installations(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать загрузки", http.StatusServiceUnavailable)
		return
	}
	for _, item := range items {
		if item.ID == id {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(22 * time.Minute))
			message := "Загрузка подготовлена"
			if err := s.prepareDownload(r.Context(), item, requestInfo(r).User.ID); err != nil {
				message = "Подготовка не завершена; подробности в строке загрузки."
			}
			http.Redirect(w, r, "/installations?message="+url.QueryEscape(message), http.StatusSeeOther)
			return
		}
	}
	http.NotFound(w, r)
}
