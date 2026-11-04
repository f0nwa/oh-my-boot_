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

// The browser provides a catalog identity only; the server resolves the storage root.
func (s *Server) inspectUbuntuISO(w http.ResponseWriter, r *http.Request) {
	writer, ok := s.Store.(ubuntuSourceCreator)
	if !ok || s.Images == nil || s.UbuntuISO == nil {
		http.Error(w, "Проверка Ubuntu ISO недоступна", http.StatusServiceUnavailable)
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
	selected, err := SelectWindowsISO(catalog, storageID, r.FormValue("path"), "Ubuntu Desktop", "")
	if err != nil {
		http.Error(w, "Исходный ISO недоступен", http.StatusBadRequest)
		return
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(16 * time.Minute))
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	inspection, err := s.UbuntuISO.InspectUbuntuISO(ctx, agent.WindowsISORequest{ID: storageID, StorageRoot: selected.StorageRoot, RelativePath: selected.Path, Size: selected.Size, Modified: selected.Modified})
	if err != nil || !ubuntuSourceDigest.MatchString(inspection.SHA256) || len(inspection.Sources) == 0 {
		http.Error(w, "Не удалось проверить Ubuntu Desktop amd64 в выбранном ISO. Проверьте файл и доступность хранилища.", http.StatusUnprocessableEntity)
		return
	}
	profile, err := writer.CreateUbuntuSource(ctx, storageID, selected.Path, inspection.SHA256, "Ubuntu Desktop")
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
