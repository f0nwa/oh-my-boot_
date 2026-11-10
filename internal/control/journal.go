package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
)

// journalView is the «Хранение журнала» block of the Journal page.
type journalView struct {
	boot.JournalState
	Choices []journalChoice
}

type journalChoice struct {
	Days  int
	Label string
}

func journalChoices() []journalChoice {
	out := make([]journalChoice, 0, len(boot.RetentionDays))
	for _, days := range boot.RetentionDays {
		label := "Не удалять"
		switch days {
		case 0:
		case 7:
			label = "Старше 7 дней"
		case 30:
			label = "Старше 30 дней"
		case 90:
			label = "Старше 90 дней"
		case 180:
			label = "Старше 180 дней"
		case 365:
			label = "Старше года"
		}
		out = append(out, journalChoice{Days: days, Label: label})
	}
	return out
}

// journalClearedAt is when the request log was last cleared by hand, if ever.
func (s *Server) journalClearedAt(ctx context.Context) time.Time {
	if s.Boot == nil {
		return time.Time{}
	}
	state, err := s.Boot.JournalSettings(ctx)
	if err != nil || state.ClearedAt == nil {
		return time.Time{}
	}
	return *state.ClearedAt
}

// dhcpEvents reads the dnsmasq log. journald keeps it on its own schedule,
// so lines from before a manual clear of the request log are dropped here;
// otherwise cleared machines would come back from their DHCP lines.
func (s *Server) dhcpEvents(ctx context.Context) ([]agent.DHCPEvent, error) {
	if s.Agent == nil {
		return nil, fmt.Errorf("agent unavailable")
	}
	events, err := s.Agent.DHCPEvents(ctx)
	if err != nil {
		return nil, err
	}
	var cleared time.Time
	if s.Boot != nil {
		state, err := s.Boot.JournalSettings(ctx)
		if err != nil {
			return nil, fmt.Errorf("journal clear boundary unavailable: %w", err)
		}
		if state.ClearedAt != nil {
			cleared = *state.ClearedAt
		}
	}
	if cleared.IsZero() {
		return events, nil
	}
	kept := events[:0]
	for _, e := range events {
		if !e.At.Before(cleared) {
			kept = append(kept, e)
		}
	}
	return kept, nil
}

// journalRetention changes automatic cleanup of the request log.
func (s *Server) journalRetention(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	days, err := strconv.Atoi(r.PostFormValue("days"))
	if err != nil || s.Boot == nil {
		http.Error(w, "Неверный срок хранения", http.StatusBadRequest)
		return
	}
	if err := s.Boot.SetJournalRetention(r.Context(), days); err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "journal.retention", "error", strconv.Itoa(days))
		http.Error(w, "Не удалось изменить срок хранения", http.StatusBadRequest)
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "journal.retention", "ok", strconv.Itoa(days))
	http.Redirect(w, r, "/events?message="+url.QueryEscape("Срок хранения журнала изменён"), http.StatusSeeOther)
}

// journalClear deletes the whole request log and the finished WinPE share
// sessions.
func (s *Server) journalClear(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	if s.Boot == nil {
		http.Error(w, "Служба Boot недоступна", http.StatusServiceUnavailable)
		return
	}
	deleted, err := s.Boot.ClearJournal(r.Context())
	if err != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "journal.clear", "error", "")
		http.Error(w, "Не удалось очистить журнал", http.StatusServiceUnavailable)
		return
	}
	if s.Agent != nil {
		_ = s.Agent.ClearSMBSessions(r.Context())
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "journal.clear", "ok", strconv.FormatInt(deleted, 10))
	http.Redirect(w, r, "/events?message="+url.QueryEscape("Журнал очищен"), http.StatusSeeOther)
}
