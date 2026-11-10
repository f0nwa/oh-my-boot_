package control

import (
	_ "embed"
	"net/http"
)

//go:embed system.js
var systemScriptBytes []byte

func systemScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(systemScriptBytes)
}
