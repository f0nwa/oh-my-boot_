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
	"omb/internal/boot"
)

const (
	dhcpFloodWindow        = 2 * time.Minute
	dhcpFloodThreshold     = 10
	smbCopyStaleAfter      = 5 * time.Minute
	smbWinPEStaleAfter     = 30 * time.Minute
	httpTransferStaleAfter = 2 * time.Minute
)

// SMBSessionDisconnectAPI closes an active client connection on the install share.
type SMBSessionDisconnectAPI interface {
	DisconnectSMBSession(ctx context.Context, id, ip, pid string) error
}

// BootTransferCancelAPI aborts an in-flight HTTP file transfer in omb-boot.
type BootTransferCancelAPI interface {
	CancelTransfer(ctx context.Context, id int) error
}

// activeSMBSession describes a currently connected WinPE SMB session for a machine.
type activeSMBSession struct {
	ID          string
	IP          string
	PID         string
	Profile     string
	CopyRead    int64
	WIMSize     int64
	Copying     bool
	Stale       bool
	IdleMinutes int
	Started     time.Time
	LastIOAt    time.Time
}

// activeHTTPTransfer describes a currently running HTTP transfer for a machine.
type activeHTTPTransfer struct {
	ID          int
	Path        string
	RemoteAddr  string
	Bytes       int64
	Size        int64
	Stale       bool
	IdleMinutes int
}

func (s *Server) smbDisconnectAPI() SMBSessionDisconnectAPI {
	if !isNilProvider(s.SMBDisconnect) {
		return s.SMBDisconnect
	}
	if s.Agent != nil {
		return s.Agent
	}
	return nil
}

func (s *Server) bootTransferCancelAPI() BootTransferCancelAPI {
	if !isNilProvider(s.TransferCancel) {
		return s.TransferCancel
	}
	if s.Boot != nil {
		return s.Boot
	}
	return nil
}

// loadRawSMBSessions fetches the raw SMB watcher snapshot from Agent.
func (s *Server) loadRawSMBSessions(ctx context.Context) (agent.SMBSessions, bool) {
	if s.Agent == nil {
		return agent.SMBSessions{}, false
	}
	v, err := s.Agent.SMBSessions(ctx)
	if err != nil {
		return agent.SMBSessions{}, false
	}
	return v, true
}

// evaluateActiveSMB computes staleness for one active (Ended == nil) SMB session.
func evaluateActiveSMB(s agent.SMBSession, now time.Time) activeSMBSession {
	ref := s.Started
	if s.LastIOAt != nil && !s.LastIOAt.IsZero() {
		ref = *s.LastIOAt
	}
	copying := s.Copying != nil && s.Copied == nil
	limit := smbWinPEStaleAfter
	if copying {
		limit = smbCopyStaleAfter
	}
	idle := now.Sub(ref)
	if idle < 0 {
		idle = 0
	}
	// Once install.wim has been copied and closed, Windows Setup reboots the
	// machine and never reads from the share again. A lingering smbd session
	// waiting on TCP keepalive is not a stalled installation.
	stale := idle >= limit && (s.Copied == nil || s.WIMOpen)
	return activeSMBSession{
		ID:          s.ID,
		IP:          s.IP,
		PID:         s.PID,
		Profile:     s.Profile,
		CopyRead:    s.CopyRead,
		WIMSize:     s.WIMSize,
		Copying:     copying,
		Stale:       stale,
		IdleMinutes: int(idle.Minutes()),
		Started:     s.Started,
		LastIOAt:    ref,
	}
}

// attachActiveSMBSessions attaches live SMB share sessions to machine rows
// and marks those that stopped transferring data beyond the staleness threshold.
func attachActiveSMBSessions(rows []machineRow, smb agent.SMBSessions, httpEvents []boot.Event, now time.Time) {
	if len(rows) == 0 || len(smb.Sessions) == 0 {
		return
	}
	byMAC := map[string]*activeSMBSession{}
	byIP := map[string]*activeSMBSession{}
	for _, s := range smb.Sessions {
		if s.Ended != nil || (s.Copied != nil && !s.WIMOpen) {
			continue
		}
		info := evaluateActiveSMB(s, now)
		if prev, ok := byIP[s.IP]; !ok || info.Started.After(prev.Started) {
			cp := info
			byIP[s.IP] = &cp
		}
		mac := ""
		best := macMatchWindow + 1
		for _, e := range httpEvents {
			if e.MAC == "" || eventIP(e.RemoteAddr) != s.IP || e.At.After(s.Started) {
				continue
			}
			if d := s.Started.Sub(e.At); d <= macMatchWindow && d < best {
				best, mac = d, e.MAC
			}
		}
		if mac != "" {
			if prev, ok := byMAC[mac]; !ok || info.Started.After(prev.Started) {
				cp := info
				byMAC[mac] = &cp
			}
		}
	}
	for i := range rows {
		if act, ok := byMAC[rows[i].MAC]; ok {
			rows[i].ActiveSMB = act
		} else if rows[i].IP != "" {
			if act, ok := byIP[rows[i].IP]; ok {
				rows[i].ActiveSMB = act
			}
		}
	}
}

// attachActiveHTTPTransfers computes staleness on currently running HTTP transfers.
func attachActiveHTTPTransfers(rows []machineRow, transfers []boot.Transfer, now time.Time) {
	for i := range rows {
		for j := range transfers {
			tr := transfers[j]
			if rows[i].IP == "" || eventIP(tr.RemoteAddr) != rows[i].IP {
				continue
			}
			ref := tr.Started
			if !tr.LastActivity.IsZero() {
				ref = tr.LastActivity
			}
			idle := now.Sub(ref)
			if idle < 0 {
				idle = 0
			}
			rows[i].ActiveHTTP = &activeHTTPTransfer{
				ID:          tr.ID,
				Path:        tr.Path,
				RemoteAddr:  tr.RemoteAddr,
				Bytes:       tr.Bytes,
				Size:        tr.Size,
				Stale:       idle >= httpTransferStaleAfter,
				IdleMinutes: int(idle.Minutes()),
			}
		}
	}
}

// detectDHCPFlood flags machines stuck at pure DHCP that send at least
// dhcpFloodThreshold DHCP requests within dhcpFloodWindow without reaching TFTP/HTTP.
func detectDHCPFlood(rows []machineRow, dhcp []agent.DHCPEvent, now time.Time) {
	if len(rows) == 0 || len(dhcp) == 0 {
		return
	}
	cutoff := now.Add(-dhcpFloodWindow)
	counts := map[string]int{}
	for _, ev := range dhcp {
		if ev.MAC == "" || strings.HasPrefix(ev.Kind, "tftp-") {
			continue
		}
		if !ev.At.Before(cutoff) && !ev.At.After(now.Add(time.Minute)) {
			counts[ev.MAC]++
		}
	}
	for i := range rows {
		if rows[i].Stage != "pxe" {
			continue
		}
		if c := counts[rows[i].MAC]; c >= dhcpFloodThreshold {
			rows[i].DHCPFlood = true
			rows[i].DHCPFloodCount = c
		}
	}
}

func safeSessionReturnPath(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" {
		return fallback
	}
	return u.Path
}

func redirectWithNotice(w http.ResponseWriter, r *http.Request, target, message string) {
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	http.Redirect(w, r, target+sep+"message="+url.QueryEscape(message), http.StatusSeeOther)
}

func (s *Server) disconnectSMBSession(w http.ResponseWriter, r *http.Request) {
	api := s.smbDisconnectAPI()
	if api == nil {
		http.Error(w, "Network Agent недоступен", http.StatusServiceUnavailable)
		return
	}
	sessionID := strings.TrimSpace(r.PostFormValue("session_id"))
	ip := strings.TrimSpace(r.PostFormValue("ip"))
	pid := strings.TrimSpace(r.PostFormValue("pid"))
	mac := strings.ToLower(strings.TrimSpace(r.PostFormValue("mac")))
	if sessionID == "" && ip == "" && pid == "" {
		http.Error(w, "Не указана SMB-сессия", http.StatusBadRequest)
		return
	}
	if err := api.DisconnectSMBSession(r.Context(), sessionID, ip, pid); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "session.smb_disconnect", "error", fmt.Sprintf("mac=%s ip=%s id=%s", mac, ip, sessionID))
		http.Error(w, "Не удалось сбросить SMB-сессию", http.StatusBadGateway)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "session.smb_disconnect", "ok", fmt.Sprintf("mac=%s ip=%s id=%s", mac, ip, sessionID))
	target := safeSessionReturnPath(r.PostFormValue("return_to"), "/")
	label := ip
	if label == "" {
		label = mac
	}
	if label == "" {
		label = sessionID
	}
	redirectWithNotice(w, r, target, fmt.Sprintf("SMB-сессия клиента %s сброшена", label))
}

func (s *Server) cancelHTTPTransfer(w http.ResponseWriter, r *http.Request) {
	api := s.bootTransferCancelAPI()
	if api == nil {
		http.Error(w, "Служба Boot недоступна", http.StatusServiceUnavailable)
		return
	}
	id, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("transfer_id")))
	if err != nil || id <= 0 {
		http.Error(w, "Не указана HTTP-передача", http.StatusBadRequest)
		return
	}
	mac := strings.ToLower(strings.TrimSpace(r.PostFormValue("mac")))
	if err := api.CancelTransfer(r.Context(), id); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "session.http_cancel", "error", fmt.Sprintf("mac=%s id=%d", mac, id))
		http.Error(w, "Не удалось прервать HTTP-передачу", http.StatusBadGateway)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "session.http_cancel", "ok", fmt.Sprintf("mac=%s id=%d", mac, id))
	target := safeSessionReturnPath(r.PostFormValue("return_to"), "/")
	msg := "HTTP-передача прервана"
	if mac != "" {
		msg = fmt.Sprintf("HTTP-передача для %s прервана", mac)
	}
	redirectWithNotice(w, r, target, msg)
}

func (s *Server) machineDHCPIgnore(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(strings.TrimSpace(r.PathValue("mac")))
	if !macAddress.MatchString(mac) {
		http.Error(w, "Неверный MAC-адрес", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(r.PostFormValue("ignored"))
	if raw != "1" && raw != "0" && raw != "true" && raw != "false" {
		http.Error(w, "Неверное значение флага игнорирования DHCP", http.StatusBadRequest)
		return
	}
	ignored := raw == "1" || raw == "true"
	book, ok := s.Store.(MachineDHCPIgnoreBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	saved, err := s.mutateBootDrivers(r.Context(), func() error {
		return book.SetMachineDHCPIgnored(r.Context(), mac, ignored)
	})
	action := "machine.dhcp_ignore"
	successMsg := fmt.Sprintf("Устройство %s добавлено в игнор DHCP", mac)
	if !ignored {
		action = "machine.dhcp_unignore"
		successMsg = fmt.Sprintf("Устройство %s снова разрешено в DHCP", mac)
	}
	result, message := driverMutationResult(saved, err, successMsg)
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, action, result, mac)
	if !saved {
		http.Error(w, "Не удалось обновить правило DHCP", http.StatusServiceUnavailable)
		return
	}
	s.bookCache.forget(mac)
	target := safeSessionReturnPath(r.PostFormValue("return_to"), "/events/machines/"+mac)
	redirectWithNotice(w, r, target, message)
}
