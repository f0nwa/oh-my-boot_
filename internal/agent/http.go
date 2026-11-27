package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"omb/internal/drivers"
	"omb/internal/nfsstorage"
	"omb/internal/smbstorage"

	"omb/internal/network"
)

// Handler exposes the agent's local control protocol.
func Handler(applier *Applier) http.Handler {
	return HandlerWithSMB(applier, nil)
}

func HandlerWithSMB(applier *Applier, manager *SMBManager) http.Handler {
	return HandlerWithManagers(applier, manager, nil)
}

func HandlerWithManagers(applier *Applier, manager *SMBManager, windows *WindowsISOManager) http.Handler {
	return HandlerWithNFS(HandlerWithUbuntuISO(HandlerWithDriverManager(applier, manager, windows, nil), nil), nil)
}

func HandlerWithDriverManager(applier *Applier, manager *SMBManager, windows *WindowsISOManager, driverManager *DriverManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /network/boot-drivers", func(w http.ResponseWriter, r *http.Request) {
		var rules []network.BootDriverRule
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&rules); err != nil || rules == nil {
			http.Error(w, "invalid boot driver request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid boot driver request", http.StatusBadRequest)
			return
		}
		if err := network.ValidateBootDriverRules(rules); err != nil {
			http.Error(w, "invalid boot driver rules", http.StatusBadRequest)
			return
		}
		if err := applier.UpdateBootDrivers(r.Context(), rules); err != nil {
			http.Error(w, "boot driver apply failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
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

	mux.HandleFunc("POST /winpe-drivers/remove-package", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver packages unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			SHA256 string `json:"sha256"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if !validDriverHash(req.SHA256) {
			http.Error(w, "invalid driver package hash", http.StatusBadRequest)
			return
		}
		if err := driverManager.RemovePackage(r.Context(), req.SHA256); err != nil {
			if errors.Is(err, ErrDriverPackageBusy) {
				http.Error(w, "driver package is being exported", http.StatusConflict)
				return
			}
			http.Error(w, "driver package cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /winpe-drivers/export", func(w http.ResponseWriter, r *http.Request) {
		if driverManager == nil {
			http.Error(w, "driver packages unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Items []DriverExportItem `json:"items"`
		}
		if !decodeAgentRequest(w, r, &req, 256<<10) {
			return
		}
		exp, err := driverManager.PrepareExport(r.Context(), req.Items)
		if err != nil {
			switch {
			case errors.Is(err, ErrInvalidDriverExport):
				http.Error(w, err.Error(), http.StatusBadRequest)
			case errors.Is(err, ErrDriverPackageMissing):
				http.Error(w, err.Error(), http.StatusNotFound)
			default:
				http.Error(w, "driver export unavailable: "+err.Error(), http.StatusServiceUnavailable)
			}
			return
		}
		defer exp.Close()
		w.Header().Set("Content-Type", "application/zip")
		_ = exp.Stream(r.Context(), w)
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
	mux.HandleFunc("POST /windows-answer/xml", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows answer unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsAnswerCheck
		if !decodeAgentRequest(w, r, &req, 8<<10) {
			return
		}
		xml, err := windows.AnswerXML(r.Context(), req)
		switch {
		case errors.Is(err, errAnswerMissing):
			http.Error(w, "Windows answer file not found", http.StatusNotFound)
			return
		case errors.Is(err, errAnswerHashDiffers):
			http.Error(w, "Windows answer file differs", http.StatusConflict)
			return
		case err != nil:
			http.Error(w, "invalid answer asset identity", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(xml)
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
		result, err := windows.Check(r.Context(), req)
		if err != nil {
			http.Error(w, "Windows ISO profile unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
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
		if _, err := resolveWindowsISOSourceRoots(windows.allowedRoots(), req, windows.LocalRoot); err != nil {
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
	mux.HandleFunc("POST /windows-iso/rebind", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows ISO rebind unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsISORequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if _, err := resolveWindowsISOSourceRoots(windows.allowedRoots(), req, windows.LocalRoot); err != nil {
			http.Error(w, "invalid Windows ISO source", http.StatusBadRequest)
			return
		}
		result, err := windows.Rebind(r.Context(), req)
		if err != nil {
			http.Error(w, "Windows ISO rebind failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /windows/profile/inspect-boot", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows boot image inspection unavailable", http.StatusServiceUnavailable)
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
		info, err := windows.InspectProfileBootWIM(r.Context(), req.ID)
		if err != nil {
			http.Error(w, "Windows profile boot.wim inspection failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
	mux.HandleFunc("POST /windows/boot-image/prepare", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows boot image preparation unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsBootImageRequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if _, err := resolveWindowsSourceFile(windows.allowedRoots(), req.ID, req.Size, req.Modified, req.StorageRoot, req.RelativePath, []string{".wim"}, windows.LocalRoot); err != nil {
			http.Error(w, "invalid Windows boot image source", http.StatusBadRequest)
			return
		}
		result, err := windows.PrepareBootImage(r.Context(), req)
		if err != nil {
			if errors.Is(err, ErrInvalidWIMType) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "Windows boot image preparation failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /windows/boot-image/remove", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows boot image unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if req.ID <= 0 {
			http.Error(w, "invalid Windows boot image ID", http.StatusBadRequest)
			return
		}
		if err := windows.RemoveBootImage(r.Context(), req.ID); err != nil {
			http.Error(w, "Windows boot image cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows/install-image/prepare", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows install image preparation unavailable", http.StatusServiceUnavailable)
			return
		}
		var req WindowsInstallImageRequest
		if !decodeSMBRequest(w, r, &req) {
			return
		}
		if _, err := resolveWindowsSourceFile(windows.allowedRoots(), req.ID, req.Size, req.Modified, req.StorageRoot, req.RelativePath, []string{".wim", ".esd"}, windows.LocalRoot); err != nil {
			http.Error(w, "invalid Windows install image source", http.StatusBadRequest)
			return
		}
		result, err := windows.PrepareInstallImage(r.Context(), req)
		if err != nil {
			if errors.Is(err, ErrInvalidWIMType) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "Windows install image preparation failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /windows/install-image/remove", func(w http.ResponseWriter, r *http.Request) {
		if windows == nil {
			http.Error(w, "Windows install image unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if !decodeAgentRequest(w, r, &req, 4096) {
			return
		}
		if req.ID <= 0 {
			http.Error(w, "invalid Windows install image ID", http.StatusBadRequest)
			return
		}
		if err := windows.RemoveInstallImage(r.Context(), req.ID); err != nil {
			http.Error(w, "Windows install image cleanup failed", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /smb/accounts", func(w http.ResponseWriter, _ *http.Request) {
		if manager == nil {
			http.Error(w, "SMB unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manager.ListAccounts())
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
		var stoppedProfiles []string
		if windows != nil && manager.MountRoot != "" && manager.UnitDir != "" {
			target := filepath.Join(manager.MountRoot, req.Key)
			if _, err := os.Lstat(filepath.Join(manager.UnitDir, smbUnitName(target))); err == nil {
				stoppedProfiles, _ = windows.StopProfilesOn(r.Context(), []string{target})
			}
		}
		if err := manager.Mount(r.Context(), req); err != nil {
			http.Error(w, "SMB mount failed", http.StatusServiceUnavailable)
			return
		}
		if windows != nil && len(stoppedProfiles) > 0 {
			_, _ = windows.RecoverProfiles(r.Context())
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
		_ = json.NewEncoder(w).Encode(struct {
			Status
			DriverUpdates []DriverScriptUpdate `json:"driver_updates,omitempty"`
		}{
			Status:        applier.Status(),
			DriverUpdates: applier.LastDriverUpdates(),
		})
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

// HandlerWithNFS exposes NFS storage operations over the agent socket.
func HandlerWithNFS(inner http.Handler, nfs *NFSManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /nfs/mount", func(w http.ResponseWriter, r *http.Request) {
		if nfs == nil {
			http.Error(w, "NFS unavailable", http.StatusServiceUnavailable)
			return
		}
		var req NFSMountRequest
		if !decodeAgentRequest(w, r, &req, 8<<10) {
			return
		}
		if nfsstorage.Validate(req.Key, req.Server, req.ExportPath, req.Version) != nil {
			http.Error(w, "invalid NFS request", http.StatusBadRequest)
			return
		}
		result, err := nfs.Mount(r.Context(), req)
		if err != nil {
			http.Error(w, "NFS mount failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /nfs/enable-upload", func(w http.ResponseWriter, r *http.Request) {
		if nfs == nil {
			http.Error(w, "NFS unavailable", http.StatusServiceUnavailable)
			return
		}
		var req NFSMountRequest
		if !decodeAgentRequest(w, r, &req, 8<<10) {
			return
		}
		if nfsstorage.Validate(req.Key, req.Server, req.ExportPath, req.Version) != nil {
			http.Error(w, "invalid NFS request", http.StatusBadRequest)
			return
		}
		if err := nfs.EnableUpload(r.Context(), req); err != nil {
			http.Error(w, "NFS upload mount failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /nfs/check-upload", func(w http.ResponseWriter, r *http.Request) {
		if nfs == nil {
			http.Error(w, "NFS unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Key string `json:"key"`
		}
		if !decodeAgentRequest(w, r, &req, 4<<10) {
			return
		}
		if nfsstorage.Validate(req.Key, "127.0.0.1", "/placeholder", "4.1") != nil {
			http.Error(w, "invalid NFS request", http.StatusBadRequest)
			return
		}
		writable, err := nfs.CheckUpload(r.Context(), req.Key)
		if err != nil {
			http.Error(w, "NFS check upload failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Writable bool `json:"writable"`
		}{Writable: writable})
	})
	mux.HandleFunc("POST /nfs/remove", func(w http.ResponseWriter, r *http.Request) {
		if nfs == nil {
			http.Error(w, "NFS unavailable", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Key string `json:"key"`
		}
		if !decodeAgentRequest(w, r, &req, 4<<10) {
			return
		}
		if nfsstorage.Validate(req.Key, "127.0.0.1", "/placeholder", "4.1") != nil {
			http.Error(w, "invalid NFS request", http.StatusBadRequest)
			return
		}
		if err := nfs.Remove(r.Context(), req.Key); err != nil {
			http.Error(w, "NFS cleanup failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", inner)
	return mux
}

// HandlerWithSystemTime exposes host timezone and NTP inspection and updates.
func HandlerWithSystemTime(inner http.Handler, manager *SystemTimeManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /system/time", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "system time unavailable", http.StatusServiceUnavailable)
			return
		}
		state, err := manager.State(r.Context())
		if err != nil {
			http.Error(w, "system time read failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("POST /system/time", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "system time unavailable", http.StatusServiceUnavailable)
			return
		}
		var req SystemTimeUpdate
		if !decodeAgentRequest(w, r, &req, 8<<10) {
			return
		}
		state, err := manager.Apply(r.Context(), req)
		if err != nil {
			if errors.Is(err, ErrInvalidSystemTime) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			http.Error(w, "system time apply failed: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
	})
	mux.Handle("/", inner)
	return mux
}

// HandlerWithLocalSMB exposes local SMB management operations over the agent socket.
func HandlerWithLocalSMB(next http.Handler, manager *LocalSMBManager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /local-smb/config", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "local SMB unavailable", http.StatusServiceUnavailable)
			return
		}
		var cfg LocalSMBConfig
		if !decodeAgentRequest(w, r, &cfg, 64<<10) {
			return
		}
		for _, u := range cfg.Users {
			if u.ID <= 0 {
				http.Error(w, "invalid user ID", http.StatusBadRequest)
				return
			}
			if !validLocalSMBUsername.MatchString(u.Username) {
				http.Error(w, "invalid username", http.StatusBadRequest)
				return
			}
			if u.Mode != "ro" && u.Mode != "rw" {
				http.Error(w, "invalid user access mode", http.StatusBadRequest)
				return
			}
		}
		if err := manager.ApplyConfig(r.Context(), cfg); err != nil {
			http.Error(w, "local SMB apply failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /local-smb/password", func(w http.ResponseWriter, r *http.Request) {
		if manager == nil {
			http.Error(w, "local SMB unavailable", http.StatusServiceUnavailable)
			return
		}
		var req LocalSMBPasswordRequest
		if !decodeAgentRequest(w, r, &req, 4<<10) {
			return
		}
		if req.ID <= 0 {
			http.Error(w, "invalid user ID", http.StatusBadRequest)
			return
		}
		if !validLocalSMBUsername.MatchString(req.Username) {
			http.Error(w, "invalid username", http.StatusBadRequest)
			return
		}
		if len(req.Password) < 12 || strings.ContainsAny(req.Password, "\r\n") {
			http.Error(w, "invalid password", http.StatusBadRequest)
			return
		}
		if err := manager.SetPassword(r.Context(), req); err != nil {
			http.Error(w, "set local SMB password failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.Handle("/", next)
	return mux
}
