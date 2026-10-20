package control

import (
	"net/http"
	"strings"
)

// translateErrors translates plain-text error responses of a handler into the
// user's language, so existing http.Error calls need no changes.
func translateErrors(lang string, next http.HandlerFunc) http.HandlerFunc {
	if lang == langRU {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		capture := &errorCapture{target: w, header: http.Header{}}
		next(capture, r)
		if !capture.wroteHeader {
			for key, values := range capture.header {
				w.Header()[key] = values
			}
			return
		}
		if !capture.captured {
			return
		}
		for key, values := range capture.header {
			w.Header()[key] = values
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(capture.status)
		_, _ = w.Write([]byte(translate(lang, strings.TrimSpace(capture.body.String())) + "\n"))
	}
}

// setLanguage stores the chosen interface language and returns to the page.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	language := r.FormValue("language")
	if language != langRU && language != langEN {
		http.Error(w, "Неверная форма", http.StatusBadRequest)
		return
	}
	info := requestInfo(r)
	if err := s.Store.SetUserLanguage(r.Context(), info.User.ID, language); err != nil {
		http.Error(w, "Не удалось сохранить язык", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, safeReturnPath(r.FormValue("return")), http.StatusSeeOther)
}

// safeReturnPath accepts only local admin paths, never another host.
func safeReturnPath(path string) string {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n?#") || strings.Contains(path, "://") {
		return "/"
	}
	for _, allowed := range []string{"/", "/account", "/events", "/network", "/menu", "/users", "/audit", "/images", "/drivers", "/answers", "/storages"} {
		if path == allowed {
			return path
		}
	}
	return "/"
}
