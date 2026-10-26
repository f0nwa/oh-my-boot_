package agent

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"pxe-platform/internal/ueficausb"
)

// UEFICAUSBRequest asks for the "enable the Microsoft UEFI CA key" USB stick of a profile.
type UEFICAUSBRequest struct {
	ProfileID int64  `json:"profile_id"`
	Loader    string `json:"loader"`
}

// FreeBytes reports the free space of the file system that holds root.
func FreeBytes(root string) (uint64, error) { return driverFreeBytes(root) }

// HandlerWithUEFICAUSB adds POST /uefica-usb/build and GET /uefica-usb/file/{key}.
func HandlerWithUEFICAUSB(inner http.Handler, b *ueficausb.Builder) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", inner)
	mux.HandleFunc("POST /uefica-usb/build", func(w http.ResponseWriter, r *http.Request) {
		if b == nil {
			http.Error(w, "USB stick builder unavailable", http.StatusServiceUnavailable)
			return
		}
		var req UEFICAUSBRequest
		if !decodeAgentRequest(w, r, &req, 1<<10) {
			return
		}
		result, err := b.Build(r.Context(), req.ProfileID, ueficausb.Loader(req.Loader))
		if !writeUEFICAUSBError(w, err) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /uefica-usb/file/{key}", func(w http.ResponseWriter, r *http.Request) {
		if b == nil {
			http.Error(w, "USB stick builder unavailable", http.StatusServiceUnavailable)
			return
		}
		f, size, err := b.Open(r.PathValue("key"))
		if !writeUEFICAUSBError(w, err) {
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		_, _ = io.Copy(w, f)
	})
	return mux
}

// writeUEFICAUSBError answers an error and reports whether there was none.
func writeUEFICAUSBError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, ueficausb.ErrInvalid):
		http.Error(w, "invalid USB stick request", http.StatusBadRequest)
	case errors.Is(err, ueficausb.ErrMissing):
		http.Error(w, "profile ISO is not mounted or incomplete", http.StatusNotFound)
	default:
		log.Printf("uefica-usb: %v", err)
		http.Error(w, "USB stick build failed", http.StatusInternalServerError)
	}
	return false
}
