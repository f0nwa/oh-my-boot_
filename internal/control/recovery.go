package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"pxe-platform/internal/agent"
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
}

func (s *Server) storageRecoverySettings(w http.ResponseWriter, r *http.Request) {
	if s.StorageRecovery == nil {
		http.Error(w, "Восстановление подключений недоступно", http.StatusServiceUnavailable)
		return
	}
	minutes, err := strconv.Atoi(r.FormValue("interval_minutes"))
	if err != nil || minutes < 1 || minutes > 60 {
		http.Error(w, "Интервал должен быть от 1 до 60 минут", http.StatusBadRequest)
		return
	}
	cfg := agent.RecoveryConfig{Enabled: r.FormValue("enabled") != "", IntervalSeconds: minutes * 60}
	info := requestInfo(r)
	detail := fmt.Sprintf("enabled=%t interval=%ds", cfg.Enabled, cfg.IntervalSeconds)
	if _, err := s.StorageRecovery.SetStorageRecovery(r.Context(), cfg); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.update", "error", detail)
		http.Error(w, "Не удалось сохранить настройки восстановления", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "storage-recovery.update", "ok", detail)
	http.Redirect(w, r, "/images?message=Настройки+восстановления+сохранены", http.StatusSeeOther)
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
	http.Redirect(w, r, "/images?message=Проверка+подключений+выполнена", http.StatusSeeOther)
}
