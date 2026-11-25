package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
)

func answerCheckTarget(r *http.Request) (string, int64, error) {
	kind := r.PathValue("kind")
	_, err := checkFileColumn(kind)
	id, idErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || idErr != nil || id <= 0 {
		return "", 0, fmt.Errorf("invalid answer file")
	}
	return kind, id, nil
}

func (s *Server) answerCheckFile(ctx context.Context, kind string, id int64) (title, digest string, ready bool, err error) {
	if kind == "windows" {
		var file WindowsAnswerVariant
		file, err = s.Store.WindowsAnswer(ctx, id)
		return file.Title, file.SHA256, file.State == "ready", err
	}
	var file UbuntuAutoinstallFile
	file, err = s.Store.UbuntuAutoinstall(ctx, id)
	return file.Title, file.SHA256, file.State == "validated", err
}

func (s *Server) answerCheckHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	kind, id, err := answerCheckTarget(r)
	if err != nil {
		http.Error(w, "Неверный файл ответов", 400)
		return
	}
	var before int64
	if raw := r.URL.Query().Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			http.Error(w, "Неверная страница истории", 400)
			return
		}
	}
	title, digest, ready, err := s.answerCheckFile(r.Context(), kind, id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Файл ответов не найден", 404)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось прочитать файл ответов", 503)
		return
	}
	checks, err := s.Store.AnswerCheckHistory(r.Context(), kind, id, before)
	if err != nil {
		http.Error(w, "Не удалось прочитать историю проверок", 503)
		return
	}
	var next int64
	if len(checks) > 20 {
		checks = checks[:20]
		next = checks[len(checks)-1].ID
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Title     string        `json:"title"`
		SHA256    string        `json:"sha256"`
		CanRecord bool          `json:"can_record"`
		Checks    []AnswerCheck `json:"checks"`
		Next      int64         `json:"next"`
	}{title, digest, ready && Allowed(requestInfo(r).User.Role, "images"), checks, next})
}

func (s *Server) answerCheckRecord(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	kind, id, err := answerCheckTarget(r)
	if err != nil {
		http.Error(w, "Неверный файл ответов", 400)
		return
	}
	var windowsProfileID int64
	if raw := r.FormValue("windows_profile_id"); raw != "" {
		windowsProfileID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || windowsProfileID < 0 {
			http.Error(w, "Неверный профиль Windows", 400)
			return
		}
	}
	c := normalizedAnswerCheck(AnswerCheck{Kind: kind, AnswerID: id, WindowsProfileID: windowsProfileID, ProfileTitle: r.FormValue("profile_title"), SHA256: r.FormValue("sha256"), Result: r.FormValue("result"), Machine: r.FormValue("machine"), Comment: r.FormValue("comment"), UserID: requestInfo(r).User.ID})
	if !validAnswerCheck(c) {
		http.Error(w, "Проверьте результат и длину полей: машина — до 200, комментарий — до 2000 символов.", 400)
		return
	}
	err = s.Store.SaveAnswerCheck(r.Context(), c)
	if errors.Is(err, errAnswerCheckConflict) {
		http.Error(w, "Файл изменён или не готов. Обновите страницу и проверьте скрипт заново.", 409)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Файл ответов не найден или запись недоступна", 404)
		return
	}
	if err != nil {
		http.Error(w, "Не удалось сохранить результат проверки", 503)
		return
	}
	_ = s.Store.Audit(r.Context(), c.UserID, "answer-check.record", "ok", fmt.Sprintf("kind=%s id=%d sha256=%s result=%s", kind, id, c.SHA256, c.Result))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) loadAnswerChecks(ctx context.Context, data *pageData) {
	checks, err := s.Store.LatestAnswerChecks(ctx)
	data.AnswerChecksKnown = err == nil
	data.WindowsChecks = map[int64]AnswerCheck{}
	data.UbuntuChecks = map[int64]AnswerCheck{}
	data.WindowsProfileChecks = map[int64][]AnswerCheck{}
	if err != nil {
		return
	}
	for _, check := range checks {
		if check.Kind == "windows" {
			data.WindowsChecks[check.AnswerID] = check
		} else {
			data.UbuntuChecks[check.AnswerID] = check
		}
	}
	byProfile, err := s.Store.LatestAnswerChecksByProfile(ctx)
	if err != nil {
		data.AnswerChecksKnown = false
		return
	}
	for _, check := range byProfile {
		if check.Kind == "windows" {
			data.WindowsProfileChecks[check.AnswerID] = append(data.WindowsProfileChecks[check.AnswerID], check)
		}
	}
}
