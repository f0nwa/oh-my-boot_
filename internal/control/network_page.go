package control

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"omb/internal/network"
)

func networkForm(r *http.Request) network.Config {
	return network.Config{
		Mode: r.FormValue("mode"), Interface: r.FormValue("interface"), ServerIP: r.FormValue("server_ip"), Subnet: r.FormValue("subnet"),
		DHCPStart: r.FormValue("dhcp_start"), DHCPEnd: r.FormValue("dhcp_end"), BootURL: r.FormValue("boot_url"), TFTPRoot: r.FormValue("tftp_root"),
		UEFILoader: r.FormValue("uefi_loader"),
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
	err := s.Agent.Apply(r.Context(), c)
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "network.apply", result, fmt.Sprintf("mode=%s interface=%s subnet=%s", c.Mode, c.Interface, c.Subnet))
	active, readErr := s.Agent.Config(r.Context())
	data := pageData{Info: info, Network: active, HasNetwork: readErr == nil}
	if err != nil {
		data.Message = "Ошибка применения: " + err.Error() + ". Показана активная конфигурация."
	} else {
		data.Message = "Настройки применены. Показана активная конфигурация."
	}
	render(w, "network", data)
}
