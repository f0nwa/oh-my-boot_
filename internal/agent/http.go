package agent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"pxe-platform/internal/network"
)

// Handler exposes the agent's local control protocol.
func Handler(applier *Applier) http.Handler {
	mux := http.NewServeMux()
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
