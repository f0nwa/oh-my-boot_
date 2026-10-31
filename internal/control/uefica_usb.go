package control

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

// «Библиотека → USB-носители»: the WinPE USB stick that enables the
// Microsoft UEFI CA key, built from a ready ISO profile with the chosen
// boot loader. Works without JavaScript; usb.js only refreshes the summary.

//go:embed usb.js
var usbJS []byte

func usbScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(usbJS)
}

// usbProfile is a ready ISO profile offered for the stick, with the sizes of
// its archives already in the Agent's cache by loader ("2011", "2023").
type usbProfile struct {
	ID     int64
	Title  string
	Path   string
	Cached map[string]int64
}

type usbView struct {
	Profiles []usbProfile
	Selected usbProfile
	Loader   string
}

// Archive is the file name the download gets.
func (v usbView) Archive() string {
	return fmt.Sprintf("omb-uefica-%d-%s.zip", v.Selected.ID, v.Loader)
}

func (s *Server) usbPage(w http.ResponseWriter, r *http.Request) {
	data := s.libraryData(r)
	view := &usbView{Loader: "2011"}
	cached := map[int64]map[string]int64{}
	if s.UEFICAUSB != nil {
		if list, err := s.UEFICAUSB.UEFICAUSBArchives(r.Context()); err == nil {
			for _, a := range list {
				if cached[a.ProfileID] == nil {
					cached[a.ProfileID] = map[string]int64{}
				}
				cached[a.ProfileID][string(a.Loader)] = a.Size
			}
		}
	}
	for _, p := range data.WindowsProfiles {
		if p.State == "ready" {
			view.Profiles = append(view.Profiles, usbProfile{ID: p.ID, Title: p.Title, Path: p.Path, Cached: cached[p.ID]})
		}
	}
	form := r.URL.Query()
	if posted := pageForm(r); posted != nil {
		form = posted
	}
	if loader := form.Get("loader"); loader == "2011" || loader == "2023" {
		view.Loader = loader
	}
	want, _ := strconv.ParseInt(form.Get("profile_id"), 10, 64)
	for i, p := range view.Profiles {
		if i == 0 || p.ID == want {
			view.Selected = p
		}
	}
	data.USB = view
	render(w, "usb", data)
}

// ueficaUSBDownload streams the "enable the Microsoft UEFI CA key" USB stick
// archive of a ready Windows ISO profile. Errors are shown on the USB page.
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
	id, err := strconv.ParseInt(r.FormValue("profile_id"), 10, 64)
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
	// The audit row must be written even when the browser cancels the
	// download, which also cancels the request context.
	auditCtx := context.WithoutCancel(r.Context())
	result, err := s.UEFICAUSB.BuildUEFICAUSB(r.Context(), id, loader)
	var body io.ReadCloser
	var size int64
	if err == nil {
		body, size, err = s.UEFICAUSB.OpenUEFICAUSB(r.Context(), result.Key)
	}
	if err != nil {
		log.Printf("uefica-usb %s: %v", detail, err)
		_ = s.Store.Audit(auditCtx, user, "uefica-usb.download", "error", detail)
		http.Error(w, "Не удалось собрать флешку", http.StatusBadGateway)
		return
	}
	defer body.Close()
	// The archive is several hundred megabytes: lift the server's write timeout.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(2 * time.Hour))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="omb-uefica-%d-%s.zip"`, id, loader))
	outcome := "ok"
	if n, err := io.Copy(w, body); err != nil || n != size {
		outcome = "error"
	}
	_ = s.Store.Audit(auditCtx, user, "uefica-usb.download", outcome, detail)
}
