package control

import (
	_ "embed"
	"net/http"
)

//go:embed machine_page.js
var machinePageJS []byte

func machinePageScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(machinePageJS)
}
