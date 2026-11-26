package control

import (
	_ "embed"
	"net/http"
)

//go:embed upload_dock.js
var uploadDockJS []byte

func uploadDockScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(uploadDockJS)
}
