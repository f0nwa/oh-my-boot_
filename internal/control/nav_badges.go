package control

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type navBadgeSnapshot struct {
	sysErrors      int
	storageDown    int
	recoveryError  bool
	storageMaint   int
	imageError     int
	imageMaint     int
	imageMigrating int
	winpeError     int
	winpePreparing int
	winpePending   int
}

type navBadgeCache struct {
	mu    sync.RWMutex
	snap  navBadgeSnapshot
	valid bool
}

func (c *navBadgeCache) get() (navBadgeSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snap, c.valid
}

func (c *navBadgeCache) set(snap navBadgeSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snap = snap
	c.valid = true
}

// navBadges returns the latest background-computed navigation badges formatted
// for lang. Reading is strictly in-memory so page requests never block on
// unrelated services (omb-images, omb-agent, etc.).
func (s *Server) navBadges(lang string) map[string]navBadge {
	snap, ok := s.navBadgesCache.get()
	if !ok {
		return nil
	}
	return formatNavBadges(snap, lang)
}

// RefreshNavBadges recomputes the navigation badge snapshot from current
// server state and updates the in-memory cache.
func (s *Server) RefreshNavBadges(ctx context.Context) {
	snap := s.computeNavBadgeSnapshot(ctx)
	s.navBadgesCache.set(snap)
}

func (s *Server) computeNavBadgeSnapshot(ctx context.Context) navBadgeSnapshot {
	var snap navBadgeSnapshot

	// 1. Журнал (/events): активные системные сбои.
	snap.sysErrors = s.activeSystemErrorCount(ctx)

	// 2. Хранилища (/storages) и активные миграции (/images).
	if s.Images != nil {
		if catalog, err := s.Images.Catalog(ctx); err == nil {
			for _, st := range catalog.Storages {
				if st.State == "unavailable" {
					snap.storageDown++
				}
			}
		}
		if migrations, err := s.Images.ListMigrations(ctx); err == nil {
			for _, m := range migrations {
				if m != nil && (m.State == "copying" || m.State == "ready_to_rebind" || m.State == "rebinding") {
					snap.imageMigrating++
				}
			}
		}
	}
	if s.StorageRecovery != nil {
		if status, err := s.StorageRecovery.StorageRecoveryStatus(ctx); err == nil && status.LastError != "" {
			snap.recoveryError = true
		}
	}

	// 3. Обслуживание (хранилища и образы).
	maint := s.loadMaintenanceLookup(ctx)
	for _, lock := range maint.locks {
		switch lock.Scope {
		case "storage":
			snap.storageMaint++
		case "image":
			snap.imageMaint++
		}
	}

	// 4. Образы ISO (/images) и Загрузки (/installations): профили и сборки WinPE.
	if s.Store != nil {
		if profiles, err := s.Store.WindowsProfiles(ctx); err == nil {
			for _, p := range profiles {
				if p.State == "error" || p.State == "unavailable" {
					snap.imageError++
				}
			}
		}
		if configs, err := s.Store.DriverConfigs(ctx); err == nil {
			for _, cfg := range configs {
				switch cfg.State {
				case "error":
					snap.winpeError++
				case "preparing":
					snap.winpePreparing++
				case "pending":
					snap.winpePending++
				}
			}
		}
	}

	return snap
}

func formatNavBadges(snap navBadgeSnapshot, lang string) map[string]navBadge {
	out := make(map[string]navBadge)
	t := func(s string) string { return translate(lang, s) }

	if snap.sysErrors > 0 {
		out["/events"] = navBadge{
			Level: "bad",
			Count: snap.sysErrors,
			Hint:  fmt.Sprintf(t("Активных системных сбоев: %d"), snap.sysErrors),
		}
	}

	if bad, acc, hint := buildPageBadge(
		[]badgePart{
			{snap.storageDown, fmt.Sprintf(t("Недоступно хранилищ: %d"), snap.storageDown)},
			{boolToInt(snap.recoveryError), t("Ошибка автовосстановления хранилищ")},
		},
		[]badgePart{
			{snap.storageMaint, fmt.Sprintf(t("Хранилищ на обслуживании: %d"), snap.storageMaint)},
		},
	); bad > 0 || acc > 0 {
		if bad > 0 {
			out["/storages"] = navBadge{Level: "bad", Count: bad, Hint: hint}
		} else {
			out["/storages"] = navBadge{Level: "accent", Count: acc, Hint: hint}
		}
	}

	if bad, acc, hint := buildPageBadge(
		[]badgePart{
			{snap.imageError, fmt.Sprintf(t("Недоступно образов: %d"), snap.imageError)},
		},
		[]badgePart{
			{snap.imageMaint, fmt.Sprintf(t("Образов на обслуживании: %d"), snap.imageMaint)},
			{snap.imageMigrating, fmt.Sprintf(t("Переносится образов: %d"), snap.imageMigrating)},
		},
	); bad > 0 || acc > 0 {
		if bad > 0 {
			out["/images"] = navBadge{Level: "bad", Count: bad, Hint: hint}
		} else {
			out["/images"] = navBadge{Level: "accent", Count: acc, Hint: hint}
		}
	}

	if bad, acc, hint := buildPageBadge(
		[]badgePart{
			{snap.winpeError, fmt.Sprintf(t("Ошибок сборки WinPE: %d"), snap.winpeError)},
		},
		[]badgePart{
			{snap.winpePreparing, fmt.Sprintf(t("Собирается WinPE: %d"), snap.winpePreparing)},
			{snap.winpePending, fmt.Sprintf(t("Ожидает сборки WinPE: %d"), snap.winpePending)},
		},
	); bad > 0 || acc > 0 {
		if bad > 0 {
			out["/installations"] = navBadge{Level: "bad", Count: bad, Hint: hint}
		} else {
			out["/installations"] = navBadge{Level: "accent", Count: acc, Hint: hint}
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

type badgePart struct {
	count int
	label string
}

func buildPageBadge(badParts, accParts []badgePart) (badCount, accCount int, hint string) {
	var hints []string
	for _, p := range badParts {
		if p.count > 0 {
			badCount += p.count
			hints = append(hints, p.label)
		}
	}
	for _, p := range accParts {
		if p.count > 0 {
			accCount += p.count
			hints = append(hints, p.label)
		}
	}
	return badCount, accCount, strings.Join(hints, "; ")
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
