package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"omb/internal/update"
)

const (
	updateUploadRoute  = "POST /system/update/upload"
	updateInstallRoute = "POST /system/update/install"
	updateIDBodyLimit  = 1024
)

var defaultUpdateClient = &http.Client{Transport: &http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		dialer := net.Dialer{Timeout: time.Second}
		return dialer.DialContext(ctx, "unix", "/run/omb-update.sock")
	},
	IdleConnTimeout: 30 * time.Second,
}}

func (s *Server) callUpdate(ctx context.Context, method, path, contentType string, body io.Reader, length int64) (update.Status, int) {
	unavailable := update.Status{State: "unavailable", Reason: "service_unavailable"}
	req, err := http.NewRequestWithContext(ctx, method, "http://omb-update"+path, body)
	if err != nil {
		return unavailable, http.StatusServiceUnavailable
	}
	if body != nil {
		req.ContentLength = length
		req.Header.Set("Content-Type", contentType)
	}
	client := s.updateClient
	if client == nil {
		client = defaultUpdateClient
	}
	res, err := client.Do(req)
	if err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			return update.Status{State: "rejected", Reason: "too_large"}, http.StatusRequestEntityTooLarge
		}
		return unavailable, http.StatusServiceUnavailable
	}
	defer res.Body.Close()
	var status update.Status
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&status); err != nil || status.State == "" {
		return unavailable, http.StatusServiceUnavailable
	}
	return status, res.StatusCode
}

func writeUpdateResponse(w http.ResponseWriter, status update.Status, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) currentUpdate(ctx context.Context) (update.Status, int) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.callUpdate(ctx, http.MethodGet, "/status", "", nil, 0)
}

func (s *Server) systemUpdateStatus(w http.ResponseWriter, r *http.Request) {
	status, code := s.currentUpdate(r.Context())
	writeUpdateResponse(w, status, code)
}

func (s *Server) systemUpdateUpload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/gzip" || r.ContentLength == 0 {
		writeUpdateResponse(w, update.Status{State: "rejected", Reason: "invalid_archive"}, http.StatusBadRequest)
		return
	}
	deadline := time.Now().Add(20 * time.Minute)
	response := http.NewResponseController(w)
	_ = response.SetReadDeadline(deadline)
	_ = response.SetWriteDeadline(deadline)
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	length := r.ContentLength
	if length < 0 {
		length = 0
	}
	status, code := s.callUpdate(ctx, http.MethodPost, "/upload", "application/gzip", r.Body, length)
	if code == http.StatusOK {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "update:verify", "ok", updateAuditDetail(status))
	}
	writeUpdateResponse(w, status, code)
}

func (s *Server) systemUpdateInstall(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	payload, err := io.ReadAll(r.Body)
	var request struct {
		ID string `json:"id"`
	}
	if err != nil || json.Unmarshal(payload, &request) != nil || request.ID == "" {
		writeUpdateResponse(w, update.Status{State: "failed", Reason: "invalid_request"}, http.StatusBadRequest)
		return
	}
	status, code := s.currentUpdate(ctx)
	if code != http.StatusOK {
		writeUpdateResponse(w, status, code)
		return
	}
	if status.State != "ready" || status.ID != request.ID {
		writeUpdateResponse(w, status, http.StatusConflict)
		return
	}
	_ = s.Store.Audit(ctx, requestInfo(r).User.ID, "update:install", "requested", updateAuditDetail(status))
	status, code = s.callUpdate(ctx, http.MethodPost, "/install", "application/json", bytes.NewReader(payload), int64(len(payload)))
	writeUpdateResponse(w, status, code)
}

func updateAuditDetail(status update.Status) string {
	return fmt.Sprintf("%s (%s → %s)", status.ID, status.Release.Current, status.Release.Target)
}
