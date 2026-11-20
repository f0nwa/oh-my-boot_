package control

import (
	_ "embed"
	"net/http"
)

//go:embed integrations.js
var integrationsJS string

func integrationsScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(integrationsJS))
}
