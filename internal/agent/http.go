package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"pxe-platform/internal/smbstorage"

	"pxe-platform/internal/network"
)

// Handler exposes the agent's local control protocol.
func Handler(applier *Applier) http.Handler {
	return HandlerWithSMB(applier, nil)
}

func HandlerWithSMB(applier *Applier, manager *SMBManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /smb/mount", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "SMB unavailable", http.StatusServiceUnavailable)
			return
		}
		var req SMBMountRequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if smbstorage.Validate(req.Key, req.Server, req.Share, req.Subpath) != nil || !validCredential(req.Username, false) || !validCredential(req.Password, false) || !validCredential(req.Domain, true) {
			http.Error(w, "invalid SMB request", http.StatusBadRequest)
			return
		}
		if err := manager.Mount(r.Context(), req); err != nil {
			http.Error(w, "SMB mount failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /smb/remove", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "SMB unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Key string `json:"key"`
		}
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if err := smbstorage.Validate(req.Key, "127.0.0.1", "placeholder", ""); err != nil {
			http.Error(w, "invalid SMB request", http.StatusBadRequest)
			return
		}
		if err := manager.Remove(r.Context(), req.Key); err != nil {
			http.Error(w, "SMB cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(applier.Status())
	})
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		config, ok := applier.CurrentConfig()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(config)
	})
	mux.HandleFunc("POST /apply", func(w http.ResponseWriter, r *http.Request) {
		var c network.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil {
			http.Error(w, "invalid JSON configuration", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "multiple JSON values are not allowed", http.StatusBadRequest)
			return
		}
		if err := network.Validate(c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := applier.Apply(r.Context(), c); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(applier.Status())
	})
	return mux
}

func decodeSMBRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		http.Error(w, "invalid SMB request", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid SMB request", http.StatusBadRequest)
		return false
	}
	return true
}
