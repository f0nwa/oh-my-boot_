package control

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"

	"omb/internal/images"
)

//go:embed hash_index.js
var hashIndexJS []byte

type hashIndexStatusClient interface {
	HashIndexStatus(context.Context) (images.HashIndexStatus, error)
}

func (s *Server) hashIndexScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(hashIndexJS)
}

func (s *Server) imagesHashIndexStatus(w http.ResponseWriter, r *http.Request) {
	var status images.HashIndexStatus
	if s.Images != nil {
		if client, ok := s.Images.(hashIndexStatusClient); ok {
			st, err := client.HashIndexStatus(r.Context())
			if err != nil {
				http.Error(w, "Не удалось получить статус индексации SHA-256", http.StatusServiceUnavailable)
				return
			}
			status = st
		} else if settings, err := s.Images.CatalogSettings(r.Context()); err == nil {
			state := "idle"
			if !settings.HashIndexEnabled {
				state = "disabled"
			}
			status = images.HashIndexStatus{
				Enabled:  settings.HashIndexEnabled,
				RateMBps: settings.HashIndexRateMBps,
				State:    state,
			}
		}
	} else {
		status = images.HashIndexStatus{State: "disabled"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(status)
}
