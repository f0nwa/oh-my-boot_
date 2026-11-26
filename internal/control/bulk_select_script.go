package control

import (
	_ "embed"
	"net/http"
)

//go:embed bulk_select.js
var bulkSelectJS []byte

func bulkSelectScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(bulkSelectJS)
}
