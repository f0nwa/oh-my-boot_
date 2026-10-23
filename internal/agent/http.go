package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"pxe-platform/internal/drivers"
	"pxe-platform/internal/smbstorage"

	"pxe-platform/internal/network"
)

// Handler exposes the agent's local control protocol.
func Handler(applier *Applier) http.Handler {
	return HandlerWithSMB(applier, nil)
}

func HandlerWithSMB(applier *Applier, manager *SMBManager) http.Handler {
	return HandlerWithManagers(applier, manager, nil)
}

func HandlerWithManagers(applier *Applier, manager *SMBManager, windows *WindowsISOManager) http.Handler {
	return HandlerWithDriverManager(applier, manager, windows, nil)
}

func HandlerWithDriverManager(applier *Applier, manager *SMBManager, windows *WindowsISOManager, driverManager *DriverManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /winpe-drivers/config-key/{id}", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid driver configuration ID", http.StatusBadRequest)
			return
		}
		key, err := driverManager.ConfigWIMKey(id)
		if err != nil {
			http.Error(w, "driver configuration manifest unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"wim_key": key})
	})
	mux.HandleFunc("POST /winpe-drivers/remove-config", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID        int64  `json:"id"`
			WIMKey    string `json:"wim_key"`
			RemoveWIM bool   `json:"remove_wim"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if req.ID <= 0 || (req.WIMKey != "" && (len(req.WIMKey) != 64 || !validManagedChild(req.WIMKey))) || (req.RemoveWIM && req.WIMKey == "") {
			http.Error(w, "invalid driver configuration identity", http.StatusBadRequest)
			return
		}
		if err := driverManager.RemoveConfig(r.Context(), req.ID, req.WIMKey, req.RemoveWIM); err != nil {
			http.Error(w, "driver configuration cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows-answer/remove", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows answer unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if req.ID <= 0 {
			http.Error(w, "invalid Windows answer ID", http.StatusBadRequest)
			return
		}
		if err := windows.RemoveAnswer(r.Context(), req.ID); err != nil {
			http.Error(w, "Windows answer cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows-iso/remove", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows ISO unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if req.ID <= 0 {
			http.Error(w, "invalid Windows profile ID", http.StatusBadRequest)
			return
		}
		if err := windows.RemoveProfile(r.Context(), req.ID); err != nil {
			http.Error(w, "Windows profile cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /winpe-drivers/import", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver import unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.ContentLength <= 0 || r.ContentLength > drivers.MaxArchiveBytes {
			http.Error(w, "invalid driver archive size", http.StatusBadRequest)
			return
		}
		inventory, err := driverManager.Import(r.Context(), http.MaxBytesReader(w, r.Body, drivers.MaxArchiveBytes), r.ContentLength)
		if err != nil {
			http.Error(w, "invalid driver package: "+err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(inventory)
	})
	mux.HandleFunc("POST /winpe-drivers/check", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver checking unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			SHA256 string `json:"sha256"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if err := driverManager.CheckPackage(r.Context(), req.SHA256); err != nil {
			http.Error(w, "driver package unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /winpe-drivers/prepare", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		var req DriverBuildRequest
		if !decodeAgentRequest(w, r, &req, 64<<10) {
			return
		}
		result, err := driverManager.PrepareConfig(r.Context(), req)
		if err != nil {
			http.Error(w, "driver configuration failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /winpe-drivers/check-config", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		var check DriverBuildCheck
		if !decodeAgentRequest(w, r, &check, 64<<10) {
			return
		}
		if err := driverManager.CheckConfig(r.Context(), check.Request, check.Result); err != nil {
			http.Error(w, "driver configuration unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows-answer/prepare", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows answer preparation unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsAnswerRequest
		if !decodeAgentRequest(w, r, &req, 2<<20) {
			return
		}
		if err := windows.PrepareAnswer(r.Context(), req); err != nil {
			http.Error(w, "Windows answer preparation failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows-answer/check", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows answer checking unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsAnswerCheck
		if !decodeAgentRequest(w, r, &req, 8<<10) {
			return
		}
		if err := windows.CheckAnswer(r.Context(), req); err != nil {
			http.Error(w, "Windows answer unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows-iso/check", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows ISO unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsISORequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if err := windows.Check(r.Context(), req); err != nil {
			http.Error(w, "Windows ISO profile unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows-iso/prepare", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows ISO preparation unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsISORequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if _, err := resolveWindowsISOSource(windows.SourceRoot, req, windows.LocalRoot); err != nil {
			http.Error(w, "invalid Windows ISO source", http.StatusBadRequest)
			return
		}
		result, err := windows.Prepare(r.Context(), req)
		if err != nil {
			http.Error(w, "Windows ISO preparation failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
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
	mux.HandleFunc("POST /smb/enable-upload", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "SMB unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Key     string `json:"key"`
			Server  string `json:"server"`
			Share   string `json:"share"`
			Subpath string `json:"subpath"`
		}
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if err := smbstorage.Validate(req.Key, req.Server, req.Share, req.Subpath); err != nil {
			http.Error(w, "invalid SMB request", http.StatusBadRequest)
			return
		}
		if err := manager.EnableUpload(r.Context(), req.Key, req.Server, req.Share, req.Subpath); err != nil {
			http.Error(w, "SMB upload mount failed", http.StatusServiceUnavailable)
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
	return decodeAgentRequest(w, r, out, 8<<10)
}

func decodeAgentRequest(w http.ResponseWriter, r *http.Request, out any, maxBytes int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
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
