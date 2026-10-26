package control

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// ueficaUSBDownload streams the "enable the Microsoft UEFI CA key" USB stick
// archive of a ready Windows ISO profile. Errors are shown on the ISO page.
func (s *Server) ueficaUSBDownload(w http.ResponseWriter, r *http.Request) {
	if s.UEFICAUSB == nil {
		http.Error(w, "Сборка флешки недоступна", http.StatusServiceUnavailable)
		return
	}
	loader := r.FormValue("loader")
	if loader != "2011" && loader != "2023" {
		http.Error(w, "Неизвестный загрузчик", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	ready := false
	if err == nil {
		profiles, err := s.Store.WindowsProfiles(r.Context())
		if err != nil {
			http.Error(w, "Не удалось прочитать профили Windows ISO", http.StatusInternalServerError)
			return
		}
		for _, p := range profiles {
			ready = ready || (p.ID == id && p.State == "ready")
		}
	}
	if !ready {
		http.Error(w, "Профиль не подготовлен", http.StatusNotFound)
		return
	}
	detail := fmt.Sprintf("%d-%s", id, loader)
	user := requestInfo(r).User.ID
	result, err := s.UEFICAUSB.BuildUEFICAUSB(r.Context(), id, loader)
	var body io.ReadCloser
	var size int64
	if err == nil {
		body, size, err = s.UEFICAUSB.OpenUEFICAUSB(r.Context(), result.Key)
	}
	if err != nil {
		log.Printf("uefica-usb %s: %v", detail, err)
		_ = s.Store.Audit(r.Context(), user, "uefica-usb.download", "error", detail)
		http.Error(w, "Не удалось собрать флешку", http.StatusBadGateway)
		return
	}
	defer body.Close()
	// The archive is several hundred megabytes: lift the server's write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Hour))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pxe-uefica-%d-%s.zip"`, id, loader))
	outcome := "ok"
	if n, err := io.Copy(w, body); err != nil || n != size {
		outcome = "error"
	}
	_ = s.Store.Audit(r.Context(), user, "uefica-usb.download", outcome, detail)
}
