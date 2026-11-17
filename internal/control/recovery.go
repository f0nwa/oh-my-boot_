package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"omb/internal/agent"
	"omb/internal/images"
)

// StorageRecoveryAPI reads and changes Agent's automatic storage recovery.
type StorageRecoveryAPI interface {
	StorageRecoveryStatus(context.Context) (agent.RecoveryStatus, error)
	SetStorageRecovery(context.Context, agent.RecoveryConfig) (agent.RecoveryStatus, error)
	RunStorageRecovery(context.Context) (agent.RecoveryStatus, error)
}

func (c *AgentClient) StorageRecoveryStatus(ctx context.Context) (agent.RecoveryStatus, error) {
	var status agent.RecoveryStatus
	err := localCall(ctx, c.http, "GET", "/storage-recovery", nil, &status)
	return status, err
}

func (c *AgentClient) SetStorageRecovery(ctx context.Context, cfg agent.RecoveryConfig) (agent.RecoveryStatus, error) {
	var status agent.RecoveryStatus
	body, err := json.Marshal(cfg)
	if err != nil {
		return status, err
	}
	err = localCall(ctx, c.http, "PUT", "/storage-recovery", body, &status)
	return status, err
}

func (c *AgentClient) RunStorageRecovery(ctx context.Context) (agent.RecoveryStatus, error) {
	var status agent.RecoveryStatus
	err := localCall(ctx, c.http, "POST", "/storage-recovery/run", nil, &status)
	return status, err
}

// loadStorageRecovery fills the recovery block of the Images page.
func (s *Server) loadStorageRecovery(ctx context.Context, data *pageData) {
	if s.StorageRecovery == nil {
		data.RecoveryError = "Состояние восстановления недоступно"
		return
	}
	status, err := s.StorageRecovery.StorageRecoveryStatus(ctx)
	if err != nil {
		data.RecoveryError = "Состояние восстановления недоступно"
		return
	}
	data.Recovery = &status
	data.RecoveryMinutes = max(1, status.IntervalSeconds/60)

	if s.Images != nil {
		if settings, err := s.Images.CatalogSettings(ctx); err == nil {
			data.RescanSettings = &settings
			data.RescanMinutes = settings.RescanIntervalMinutes
		}
	}
	if data.RescanMinutes <= 0 {
		data.RescanMinutes = 30
	}
}

func (s *Server) storageRecoverySettings(w http.ResponseWriter, r *http.Request) {
	if s.StorageRecovery == nil {
		http.Error(w, "Восстановление подключений недоступно", http.StatusServiceUnavailable)
		return
	}
	recMinutesStr := r.FormValue("recovery_interval_minutes")
	if recMinutesStr == "" {
		recMinutesStr = r.FormValue("interval_minutes")
	}
	minutes, err := strconv.Atoi(recMinutesStr)
	if err != nil || minutes < 1 || minutes > 60 {
		http.Error(w, "Интервал должен быть от 1 до 60 минут", http.StatusBadRequest)
		return
	}
	recoveryEnabled := r.FormValue("recovery_enabled") != ""
	if r.FormValue("recovery_enabled") == "" && r.FormValue("enabled") != "" {
		recoveryEnabled = true
	}
	cfg := agent.RecoveryConfig{Enabled: recoveryEnabled, IntervalSeconds: minutes * 60}
	info := requestInfo(r)
	detail := fmt.Sprintf("enabled=%t interval=%ds", cfg.Enabled, cfg.IntervalSeconds)
	if _, err := s.StorageRecovery.SetStorageRecovery(r.Context(), cfg); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.update", "error", detail)
		http.Error(w, "Не удалось сохранить настройки восстановления", http.StatusServiceUnavailable)
		return
	}

	if s.Images != nil && (r.Form.Has("rescan_interval_minutes") || r.Form.Has("rescan_enabled")) {
		rescanMinStr := r.FormValue("rescan_interval_minutes")
		rescanMin, err := strconv.Atoi(rescanMinStr)
		if err != nil || rescanMin < 1 || rescanMin > 1440 {
			http.Error(w, "Интервал сканирования должен быть от 1 до 1440 минут", http.StatusBadRequest)
			return
		}
		rescanCfg := images.CatalogSettings{
			RescanEnabled:         r.FormValue("rescan_enabled") != "",
			RescanIntervalMinutes: rescanMin,
		}
		if _, err := s.Images.SetCatalogSettings(r.Context(), rescanCfg); err != nil {
			_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.update", "error", detail+" rescan=error")
			http.Error(w, "Не удалось сохранить настройки сканирования ISO", http.StatusServiceUnavailable)
			return
		}
		detail += fmt.Sprintf(" rescan_enabled=%t rescan_interval=%dm", rescanCfg.RescanEnabled, rescanMin)
	}

	_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.update", "ok", detail)
	http.Redirect(w, r, "/storages?message=Настройки+фоновых+задач+сохранены", http.StatusSeeOther)
}

func (s *Server) storageRecoveryRun(w http.ResponseWriter, r *http.Request) {
	if s.StorageRecovery == nil {
		http.Error(w, "Восстановление подключений недоступно", http.StatusServiceUnavailable)
		return
	}
	info := requestInfo(r)
	status, err := s.StorageRecovery.RunStorageRecovery(r.Context())
	if err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.run", "error", "")
		http.Error(w, "Не удалось выполнить проверку подключений", http.StatusServiceUnavailable)
		return
	}
	result := "ok"
	if status.LastError != "" {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.run", result, fmt.Sprintf("actions=%d", len(status.Actions)))
	http.Redirect(w, r, "/storages?message=Проверка+подключений+выполнена", http.StatusSeeOther)
}
