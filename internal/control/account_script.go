package control

import (
	_ "embed"
	"net/http"
)

//go:embed account.js
var accountJS []byte

func accountScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(accountJS)
}
