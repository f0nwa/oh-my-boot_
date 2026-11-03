package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"omb/internal/network"
	"strings"
)

type EffectiveNetworkDriver struct {
	Driver, Source, Prefix string
}

func effectiveNetworkDriver(info MachineInfo, rules macRules, defaultDriver string) EffectiveNetworkDriver {
	if info.NetworkDriver != "" {
		return EffectiveNetworkDriver{Driver: info.NetworkDriver, Source: "machine"}
	}
	var best *MacRule
	for i := range rules {
		rule := &rules[i]
		if rule.NetworkDriver != "" && strings.HasPrefix(info.MAC, rule.Prefix+":") && (best == nil || len(rule.Prefix) > len(best.Prefix)) {
			best = rule
		}
	}
	if best != nil {
		return EffectiveNetworkDriver{Driver: best.NetworkDriver, Source: "prefix", Prefix: best.Prefix}
	}
	if defaultDriver == "" {
		defaultDriver = "ipxe"
	}
	return EffectiveNetworkDriver{Driver: defaultDriver, Source: "global"}
}

type BootDriverSync interface {
	SetBootDrivers(context.Context, []network.BootDriverRule) error
}

func (s *Server) SyncBootDrivers(ctx context.Context) error {
	s.driverMu.Lock()
	defer s.driverMu.Unlock()
	return s.syncBootDriversLocked(ctx)
}

func (s *Server) syncBootDriversLocked(ctx context.Context) error {
	store, ok := s.Store.(interface {
		BootDriverRules(context.Context) ([]network.BootDriverRule, error)
	})
	if !ok {
		return nil
	}
	rules, err := store.BootDriverRules(ctx)
	if err != nil {
		return err
	}
	if s.BootDrivers == nil {
		return fmt.Errorf("boot driver Agent unavailable")
	}
	return s.BootDrivers.SetBootDrivers(ctx, rules)
}

// mutateBootDrivers distinguishes a failed save from a saved but unapplied change.
func (s *Server) mutateBootDrivers(ctx context.Context, mutate func() error) (bool, error) {
	s.driverMu.Lock()
	defer s.driverMu.Unlock()
	if err := mutate(); err != nil {
		return false, err
	}
	return true, s.syncBootDriversLocked(ctx)
}

const driverApplyWarning = "Настройка сохранена, но не применена: Agent недоступен"

func driverMutationResult(saved bool, err error, success string) (string, string) {
	if !saved {
		return "error", success
	}
	if err != nil {
		return "apply_error", driverApplyWarning
	}
	return "ok", success
}

func (s *Server) machineNetworkDriver(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	driver := r.PostFormValue("network_driver")
	book, ok := s.Store.(MachineBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	if !macAddress.MatchString(mac) || !validNetworkDriverSelection(mac, driver) {
		http.Error(w, "Недопустимый сетевой драйвер", http.StatusBadRequest)
		return
	}
	saved, err := s.mutateBootDrivers(r.Context(), func() error { return book.SetMachineNetworkDriver(r.Context(), mac, driver) })
	result, message := driverMutationResult(saved, err, "Сетевой драйвер сохранён")
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "machine.network_driver", result, mac)
	if !saved {
		http.Error(w, "Не удалось сохранить сетевой драйвер", http.StatusServiceUnavailable)
		return
	}
	s.bookCache.forget(mac)
	http.Redirect(w, r, "/events/machines/"+mac+"?message="+url.QueryEscape(message), http.StatusSeeOther)
}

// Clearing a selection is always allowed, including on an older invalid record.
func validNetworkDriverSelection(match, driver string) bool {
	if driver == "" {
		return true
	}
	return network.ValidateBootDriverRules([]network.BootDriverRule{{Match: match, Driver: driver}}) == nil
}
