package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"omb/internal/agent"
)

type ubuntuSourceCreator interface {
	CreateUbuntuSource(context.Context, int64, string, string, string) (BootProfile, error)
	SetUbuntuSourceState(context.Context, int64, string, string) error
}

// Browsers submit catalog identities; only Agent determines the content type.
func (s *Server) inspectISO(w http.ResponseWriter, r *http.Request) {
	store, ok := s.Store.(imageInspectionStore)
	detector, canInspect := s.UbuntuISO.(interface {
		InspectISO(context.Context, agent.WindowsISORequest) (agent.ISOInspection, error)
	})
	if !ok || !canInspect || s.Images == nil {
		http.Error(w, "Проверка ISO недоступна", http.StatusServiceUnavailable)
		return
	}
	storageID, err := strconv.ParseInt(r.FormValue("storage_id"), 10, 64)
	if err != nil || storageID <= 0 {
		http.Error(w, "Выберите исходный ISO", http.StatusBadRequest)
		return
	}
	catalog, err := s.Images.Catalog(r.Context())
	if err != nil {
		http.Error(w, "Каталог ISO недоступен", http.StatusServiceUnavailable)
		return
	}
	selected, err := SelectWindowsISO(catalog, storageID, r.FormValue("path"), "ISO", "")
	if err != nil {
		http.Error(w, "Исходный ISO недоступен", http.StatusBadRequest)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(16 * time.Minute))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	result, probeErr := detector.InspectISO(ctx, agent.WindowsISORequest{ID: storageID, StorageRoot: selected.StorageRoot, RelativePath: selected.Path, Size: selected.Size, Modified: selected.Modified})
	kind := result.Kind
	if probeErr != nil || (kind != "windows" && kind != "strelec" && kind != "ubuntu" && kind != "unsupported") || (kind == "ubuntu" && (!ubuntuSourceDigest.MatchString(result.Ubuntu.SHA256) || len(result.Ubuntu.Sources) == 0)) {
		kind = "error"
	}
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer persistCancel()
	if err := store.SetImageInspection(persistCtx, inspectionRecord(selected, kind)); err != nil {
		http.Error(w, "Не удалось сохранить результат проверки ISO", http.StatusServiceUnavailable)
		return
	}
	switch kind {
	case "error":
		http.Error(w, "Не удалось определить поддерживаемый образ. Проверьте ISO и доступность хранилища.", http.StatusUnprocessableEntity)
		return
	case "unsupported":
		http.Error(w, "Поддерживаемая структура ISO не найдена", http.StatusUnprocessableEntity)
		return
	case "windows", "strelec":
		_ = s.Store.Audit(ctx, requestInfo(r).User.ID, "iso.inspect", "ok", fmt.Sprintf("storage=%d path=%s kind=%s", storageID, selected.Path, kind))
		target := url.Values{"storage_id": {strconv.FormatInt(storageID, 10)}, "path": {selected.Path}}
		http.Redirect(w, r, "/installations/new?"+target.Encode(), http.StatusSeeOther)
		return
	}
	writer, ok := s.Store.(ubuntuSourceCreator)
	if !ok {
		http.Error(w, "Не удалось сохранить источник ISO", http.StatusServiceUnavailable)
		return
	}
	profile, err := writer.CreateUbuntuSource(ctx, storageID, selected.Path, result.Ubuntu.SHA256, "Ubuntu Desktop")
	if err != nil {
		http.Error(w, "Не удалось сохранить источник ISO", http.StatusServiceUnavailable)
		return
	}
	if profile.UbuntuState == "unverified" || profile.UbuntuState == "checking" || profile.UbuntuState == "error" {
		if err := writer.SetUbuntuSourceState(ctx, profile.UbuntuSourceID, "verified", ""); err != nil {
			http.Error(w, "Не удалось сохранить результат проверки ISO", http.StatusServiceUnavailable)
			return
		}
	}
	_ = s.Store.Audit(ctx, requestInfo(r).User.ID, "ubuntu-source.inspect", "ok", fmt.Sprintf("id=%d", profile.UbuntuSourceID))
	target := url.Values{"boot_profile_id": {strconv.FormatInt(profile.ID, 10)}}
	http.Redirect(w, r, "/installations/new?"+target.Encode(), http.StatusSeeOther)
}
