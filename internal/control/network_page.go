package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"omb/internal/agent"
	"omb/internal/network"
)

func parseWinPEFormInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return -1
	}
	return v
}

func networkForm(r *http.Request) network.Config {
	return network.Config{
		Mode: r.FormValue("mode"), Interface: r.FormValue("interface"), ServerIP: r.FormValue("server_ip"), Subnet: r.FormValue("subnet"),
		DHCPStart: r.FormValue("dhcp_start"), DHCPEnd: r.FormValue("dhcp_end"), BootURL: r.FormValue("boot_url"), TFTPRoot: r.FormValue("tftp_root"),
		UEFILoader: r.FormValue("uefi_loader"), DefaultDriver: r.FormValue("default_driver"),
		ReuseFirmwareNetwork: r.FormValue("reuse_firmware_network") == "true",
		WinPETimeoutSeconds:  parseWinPEFormInt(r.FormValue("winpe_timeout")),
		WinPERetrySeconds:    parseWinPEFormInt(r.FormValue("winpe_retry")),
		WinPEExitAction:      strings.TrimSpace(r.FormValue("winpe_exit_action")),
	}
}

func networkConfirmation(token string, c network.Config) string {
	payload, _ := json.Marshal(c)
	sum := sha256.Sum256(append([]byte("omb-network-confirm:"+token+":"), payload...))
	return hex.EncodeToString(sum[:])
}

func (s *Server) checkedNetwork(c network.Config) error {
	if !s.AllowedInterfaces[c.Interface] {
		return fmt.Errorf("интерфейс не разрешён для PXE")
	}
	ips, err := InterfaceIPs(c.Interface)
	if err != nil {
		return err
	}
	return ValidateNetworkTarget(c, s.AllowedInterfaces, ips)
}

func (s *Server) hasPreparingWinPEAssets(ctx context.Context) (bool, error) {
	if s.Store == nil {
		return false, nil
	}
	configs, err := s.Store.DriverConfigs(ctx)
	if err != nil {
		return false, err
	}
	for _, cfg := range configs {
		if cfg.State == "preparing" {
			return true, nil
		}
	}
	profiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range profiles {
		if p.State == "preparing" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) syncWinPEDriverScripts(ctx context.Context, updates []agent.DriverScriptUpdate) error {
	if len(updates) == 0 || s.Store == nil {
		return nil
	}
	syncer, ok := s.Store.(interface {
		SyncWinPEDriverScripts(context.Context, []agent.DriverScriptUpdate) (string, error)
	})
	if !ok {
		return nil
	}
	activeScript, err := syncer.SyncWinPEDriverScripts(ctx, updates)
	if err != nil {
		return err
	}
	if activeScript != "" && s.Boot != nil {
		if _, err := s.Boot.Publish(ctx, []byte(activeScript)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) networkPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message")}
	if s.Agent != nil {
		c, err := s.Agent.Config(r.Context())
		if err == nil {
			data.Network = c
			data.HasNetwork = true
		}
	}
	render(w, "network", data)
}

func (s *Server) networkPreview(w http.ResponseWriter, r *http.Request) {
	c := networkForm(r)
	if err := s.checkedNetwork(c); err != nil {
		http.Error(w, "Недопустимая конфигурация: "+err.Error(), 400)
		return
	}
	data := pageData{Info: requestInfo(r), Network: c, Confirmation: networkConfirmation(requestInfo(r).Token, c)}
	if s.Agent != nil {
		old, err := s.Agent.Config(r.Context())
		if err == nil {
			data.OldNetwork = old
		}
	}
	render(w, "network_preview", data)
}

func (s *Server) networkApply(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	c := networkForm(r)
	expected := networkConfirmation(info.Token, c)
	if len(r.FormValue("confirmation")) != len(expected) || subtle.ConstantTimeCompare([]byte(expected), []byte(r.FormValue("confirmation"))) != 1 {
		http.Error(w, "Нужно подтвердить изменения", 403)
		return
	}
	if err := s.checkedNetwork(c); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "network.apply", "denied", c.Interface)
		http.Error(w, "Недопустимая конфигурация: "+err.Error(), 400)
		return
	}
	if s.Agent == nil {
		http.Error(w, "Network Agent недоступен", 503)
		return
	}
	if busy, err := s.hasPreparingWinPEAssets(r.Context()); err != nil {
		http.Error(w, "Не удалось проверить активные сборки WinPE", 503)
		return
	} else if busy {
		_ = s.Store.Audit(r.Context(), info.User.ID, "network.apply", "conflict", "winpe preparing")
		http.Error(w, "Нельзя изменять параметры WinPE во время подготовки образов или драйверов", http.StatusConflict)
		return
	}
	updates, err := s.Agent.ApplyWithWinPE(r.Context(), c)
	if err == nil {
		err = s.syncWinPEDriverScripts(r.Context(), updates)
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "network.apply", result, fmt.Sprintf("mode=%s interface=%s subnet=%s reuse_firmware_network=%t winpe_timeout=%d winpe_retry=%d winpe_exit_action=%s", c.Mode, c.Interface, c.Subnet, c.ReuseFirmwareNetwork, c.EffectiveWinPETimeout(), c.EffectiveWinPERetry(), c.EffectiveWinPEExitAction()))
	active, readErr := s.Agent.Config(r.Context())
	data := pageData{Info: info, Network: active, HasNetwork: readErr == nil}
	if err != nil {
		data.Message = "Ошибка применения: " + err.Error() + ". Показана активная конфигурация."
	} else {
		data.Message = "Настройки применены. Показана активная конфигурация."
	}
	render(w, "network", data)
}
