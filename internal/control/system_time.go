package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"omb/internal/agent"
)

type SystemTimeAPI interface {
	SystemTime(context.Context) (agent.SystemTimeState, error)
	SetSystemTime(context.Context, agent.SystemTimeUpdate) (agent.SystemTimeState, error)
}

type systemTimeView struct {
	NowDisplay      string
	ManualDefault   string
	Timezone        string
	NTPEnabled      bool
	NTPSynchronized bool
	CanNTP          bool
	NTPBackend      string
	NTPServersText  string
	Timezones       []string
}

var (
	serverLocMu   sync.RWMutex
	serverLoc     *time.Location
	serverLocZone string
)

// SetServerTimezone updates the in-process timezone cache after a timezone change
// so web UI timestamps immediately reflect the new host timezone without restarting omb-control.
// Passing an empty string clears the explicit override.
func SetServerTimezone(name string) {
	name = strings.TrimSpace(name)
	serverLocMu.Lock()
	defer serverLocMu.Unlock()
	if name == "" {
		serverLoc = nil
		serverLocZone = ""
		return
	}
	if loc, err := time.LoadLocation(name); err == nil {
		serverLoc = loc
		serverLocZone = name
	}
}

func serverLocation() *time.Location {
	serverLocMu.RLock()
	loc := serverLoc
	serverLocMu.RUnlock()
	if loc != nil {
		return loc
	}
	if tz := detectHostTimezone(); tz != "" {
		if loaded, err := time.LoadLocation(tz); err == nil {
			serverLocMu.Lock()
			if serverLoc == nil {
				serverLoc = loaded
				serverLocZone = tz
			}
			loc = serverLoc
			serverLocMu.Unlock()
			return loc
		}
	}
	return time.Local
}

func detectHostTimezone() string {
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		if tz := strings.TrimSpace(string(data)); tz != "" {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, after, ok := strings.Cut(target, "zoneinfo/"); ok {
			if tz := strings.TrimSpace(after); tz != "" {
				return tz
			}
		}
	}
	return ""
}

func parseNTPServersInput(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == ','
	})
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func buildSystemTimeView(st agent.SystemTimeState) *systemTimeView {
	if st.Timezone != "" {
		SetServerTimezone(st.Timezone)
	}
	loc := serverLocation()
	now := time.Now().In(loc)
	if st.NowRFC3339 != "" {
		if parsed, err := time.Parse(time.RFC3339, st.NowRFC3339); err == nil {
			now = parsed.In(loc)
		}
	}
	tzList := append([]string(nil), st.Timezones...)
	if len(tzList) == 0 {
		tzList = []string{"UTC", "Europe/Moscow", "Europe/Kaliningrad", "Europe/Samara", "Asia/Yekaterinburg", "Asia/Novosibirsk", "Asia/Krasnoyarsk", "Asia/Irkutsk", "Asia/Vladivostok"}
	}
	if st.Timezone != "" && !slices.Contains(tzList, st.Timezone) {
		tzList = append([]string{st.Timezone}, tzList...)
	}
	return &systemTimeView{
		NowDisplay:      now.Format("02.01.2006 15:04:05"),
		ManualDefault:   now.Format("2006-01-02T15:04"),
		Timezone:        st.Timezone,
		NTPEnabled:      st.NTPEnabled,
		NTPSynchronized: st.NTPSynchronized,
		CanNTP:          st.CanNTP,
		NTPBackend:      st.NTPBackend,
		NTPServersText:  strings.Join(st.NTPServers, " "),
		Timezones:       tzList,
	}
}

func (s *Server) systemTimeAPI() SystemTimeAPI {
	if s.SystemTime != nil {
		return s.SystemTime
	}
	if s.Agent != nil {
		return s.Agent
	}
	return nil
}

func (s *Server) loadSystemTime(ctx context.Context, data *pageData) {
	api := s.systemTimeAPI()
	if api == nil {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	st, err := api.SystemTime(tctx)
	if err != nil {
		data.SystemTimeError = "Служба времени недоступна"
		return
	}
	data.SystemTime = buildSystemTimeView(st)
}

func (s *Server) systemTimeUpdate(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	lang := normalizeLanguage(info.User.Language)
	api := s.systemTimeAPI()
	if api == nil {
		http.Error(w, "Служба времени недоступна", http.StatusServiceUnavailable)
		return
	}
	tz := strings.TrimSpace(r.FormValue("timezone"))
	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode != "ntp" && mode != "manual" {
		http.Error(w, "Выберите режим синхронизации времени", http.StatusBadRequest)
		return
	}
	req := agent.SystemTimeUpdate{
		Timezone:   tz,
		NTPEnabled: mode == "ntp",
	}
	if req.NTPEnabled {
		req.NTPServers = parseNTPServersInput(r.FormValue("ntp_servers"))
	} else {
		req.ManualTime = strings.TrimSpace(r.FormValue("manual_time"))
	}

	clean, err := agent.ValidateSystemTimeUpdate(req, nil)
	if err != nil {
		http.Error(w, formatSystemTimeValidationError(err), http.StatusBadRequest)
		return
	}

	updated, err := api.SetSystemTime(r.Context(), clean)
	detail := fmt.Sprintf("tz=%s mode=%s ntp=%s manual=%t", clean.Timezone, mode, strings.Join(clean.NTPServers, ","), clean.ManualTime != "")
	if err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "system.time", "error", detail)
		if errors.Is(err, agent.ErrInvalidSystemTime) {
			http.Error(w, formatSystemTimeValidationError(err), http.StatusBadRequest)
			return
		}
		http.Error(w, "Не удалось применить настройки времени сервера", http.StatusServiceUnavailable)
		return
	}

	if updated.Timezone != "" {
		SetServerTimezone(updated.Timezone)
	} else {
		SetServerTimezone(clean.Timezone)
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "system.time", "ok", detail)

	var msg string
	if clean.NTPEnabled {
		if len(clean.NTPServers) > 0 {
			msg = fmt.Sprintf(translate(lang, "Настройки времени сохранены (%s, NTP: %s)"), clean.Timezone, strings.Join(clean.NTPServers, " "))
		} else {
			msg = fmt.Sprintf(translate(lang, "Настройки времени сохранены (%s, NTP: по умолчанию ОС)"), clean.Timezone)
		}
	} else {
		msg = fmt.Sprintf(translate(lang, "Настройки времени сохранены (%s, вручную)"), clean.Timezone)
	}
	http.Redirect(w, r, "/system?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func formatSystemTimeValidationError(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "timezone"):
		return "Укажите корректный часовой пояс IANA"
	case strings.Contains(s, "too many NTP"):
		return "Можно указать не более 8 NTP-серверов"
	case strings.Contains(s, "NTP server"):
		return "Некорректный адрес NTP-сервера (допустимы IP-адреса и доменные имена)"
	case strings.Contains(s, "manual time"):
		return "Некорректная дата или время (допустимый диапазон 2020–2099)"
	case strings.Contains(s, "range 2020..2099"):
		return "Некорректная дата или время (допустимый диапазон 2020–2099)"
	default:
		return "Проверьте параметры времени и NTP"
	}
}
