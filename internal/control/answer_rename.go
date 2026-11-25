package control

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

func validAnswerScriptTitle(title string) bool {
	title = strings.TrimSpace(title)
	return title != "" && utf8.ValidString(title) && utf8.RuneCountInString(title) <= 80 && strings.IndexFunc(title, unicode.IsControl) < 0
}

// safeAnswersReturnURL validates a return_to value targeting /answers and keeps
// only safe list-view query parameters (q, sort, dir), stripping any prior message.
func safeAnswersReturnURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/answers"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path != "/answers" {
		return "/answers"
	}
	q := u.Query()
	clean := url.Values{}
	for _, key := range []string{"q", "sort", "dir"} {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			clean.Set(key, v)
		}
	}
	if len(clean) == 0 {
		return "/answers"
	}
	return "/answers?" + clean.Encode()
}

func (s *Server) answerRename(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind != "windows" && kind != "ubuntu" {
		http.Error(w, "Скрипт не найден", http.StatusNotFound)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	title := strings.TrimSpace(r.FormValue("title"))
	if err != nil || id <= 0 || !validAnswerScriptTitle(title) {
		http.Error(w, "Укажите название скрипта: до 80 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	var auditAction string
	if kind == "windows" {
		err = s.Store.SetWindowsAnswerTitle(r.Context(), id, title)
		auditAction = "windows-answer.rename"
	} else {
		err = s.Store.SetUbuntuAutoinstallTitle(r.Context(), id, title)
		auditAction = "ubuntu-autoinstall.rename"
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Скрипт не найден", http.StatusNotFound)
			return
		}
		http.Error(w, "Не удалось переименовать скрипт", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, auditAction, "ok", fmt.Sprintf("id=%d title=%s", id, title))
	msg := fmt.Sprintf("Скрипт переименован: «%s»", title)
	target := safeAnswersReturnURL(r.FormValue("return_to"))
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	http.Redirect(w, r, target+sep+"message="+url.QueryEscape(msg), http.StatusSeeOther)
}
