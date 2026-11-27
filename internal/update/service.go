package update

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

type Installer interface {
	Check(context.Context, string) (Release, error)
	Apply(string, Release, func(string)) error
	Pending() (bool, error)
	Committed(Release) bool
}

type Service struct {
	dir       string
	installer Installer
	mux       *http.ServeMux
	mu        sync.Mutex
	status    Status
	closing   bool
}

func NewService(dir string, installer Installer) (*Service, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	status, err := loadStatus(dir)
	if err != nil {
		return nil, err
	}
	s := &Service{dir: dir, installer: installer, status: status, mux: http.NewServeMux()}
	switch status.State {
	case "checking":
		status.State, status.Reason = "rejected", "interrupted"
	case "installing":
		status = s.finish(status, installer.Committed(status.Release))
	}
	if status != s.status {
		if err := s.save(status); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		matched, _ := filepath.Match("upload-*", entry.Name())
		if matched || entry.Name() == "stage" && status.State != "ready" {
			if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
				return nil, err
			}
		}
	}
	s.mux.HandleFunc("POST /upload", s.upload)
	s.mux.HandleFunc("POST /install", s.install)
	s.mux.HandleFunc("GET /status", s.getStatus)
	return s, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// BeginRestart keeps jobs attached to the current process until their result is saved.
func (s *Service) BeginRestart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.status.State == "checking" || s.status.State == "installing" {
		return false
	}
	s.closing = true
	return true
}

// save is called with the mutex held once the service starts serving requests.
func (s *Service) save(status Status) error {
	if err := writeStatus(s.dir, status); err != nil {
		log.Printf("save update status: %v", err)
		return err
	}
	s.status = status
	return nil
}

func respond(w http.ResponseWriter, code int, status Status) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Service) getStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status
	if status.State != "checking" && status.State != "installing" {
		pending, err := s.installer.Pending()
		switch {
		case pending:
			status.State, status.Reason = "recovery_required", "pending"
		case err == nil && status.State == "recovery_required":
			status.State, status.Reason = "failed", "installation_failed"
		}
	}
	respond(w, 200, status)
}

func (s *Service) checkPending(w http.ResponseWriter) bool {
	pending, err := s.installer.Pending()
	if err != nil {
		log.Printf("read deployment state: %v", err)
		respond(w, 500, Status{State: "failed", Reason: "service_error"})
		return false
	}
	if pending {
		out := s.status
		out.State, out.Reason = "recovery_required", "pending"
		respond(w, 409, out)
		return false
	}
	return true
}

func (s *Service) beginUpload(w http.ResponseWriter) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		respond(w, 503, Status{State: "unavailable"})
		return Status{}, false
	}
	if s.status.State == "checking" || s.status.State == "installing" {
		respond(w, 409, s.status)
		return Status{}, false
	}
	if !s.checkPending(w) {
		return Status{}, false
	}
	next := Status{ID: rand.Text(), State: "checking"}
	if err := s.save(next); err != nil {
		respond(w, 500, Status{State: "failed", Reason: "service_error"})
		return Status{}, false
	}
	return next, true
}

func (s *Service) upload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > MaxArchiveBytes {
		respond(w, 413, Status{State: "rejected", Reason: "too_large"})
		return
	}
	next, ok := s.beginUpload(w)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxArchiveBytes)
	release, code, reason := s.stageArchive(r.Context(), r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	next.State, next.Reason, next.Release = "ready", reason, release
	if code != 200 {
		next.State = "rejected"
	}
	if err := s.save(next); err != nil {
		next.State, next.Reason, code = "failed", "service_error", 500
		s.status = next
	}
	respond(w, code, next)
}

func (s *Service) install(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&request); err != nil || request.ID == "" {
		respond(w, 400, Status{State: "failed", Reason: "invalid_request"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		respond(w, 503, Status{State: "unavailable"})
		return
	}
	if s.status.State != "ready" || s.status.ID != request.ID {
		respond(w, 409, s.status)
		return
	}
	if !s.checkPending(w) {
		return
	}
	next := s.status
	next.State, next.Phase = "installing", "preflight"
	if err := s.save(next); err != nil {
		respond(w, 500, Status{State: "failed", Reason: "service_error"})
		return
	}
	go s.apply(next)
	respond(w, 202, next)
}

func (s *Service) apply(status Status) {
	err := s.installer.Apply(filepath.Join(s.dir, "stage", "apt-repo"), status.Release, func(phase string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		status.Phase = phase
		if err := s.save(status); err != nil {
			s.status = status
		}
	})
	if err != nil {
		log.Printf("install OMB update %s: %v", status.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status = s.finish(status, err == nil)
	if err := s.save(status); err != nil {
		s.status = status
	}
	if err := os.RemoveAll(filepath.Join(s.dir, "stage")); err != nil {
		log.Printf("remove applied update archive: %v", err)
	}
}

func (s *Service) finish(status Status, installed bool) Status {
	pending, err := s.installer.Pending()
	status.Reason = ""
	switch {
	case pending || err != nil:
		status.State, status.Reason = "recovery_required", "pending"
	case installed:
		status.State = "installed"
	case status.Phase == "rolled_back":
		status.State = "rolled_back"
	default:
		status.State, status.Reason = "failed", "installation_failed"
	}
	status.Phase = ""
	return status
}

func archiveFailure(err error) (int, string) {
	var size *http.MaxBytesError
	if errors.Is(err, errArchiveSize) || errors.As(err, &size) {
		return 413, "too_large"
	}
	return 422, "invalid_archive"
}
