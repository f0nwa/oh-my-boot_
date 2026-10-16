package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) menuPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), MenuTitle: "PXE Platform", MenuLabel: "Diagnostics", MenuMessage: "PXE Platform network boot is ready"}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать версии меню", 500)
		return
	}
	data.Versions = versions
	for _, version := range versions {
		if version.Active {
			data.MenuTitle = version.Title
			data.MenuLabel = version.Label
			data.MenuMessage = version.Message
			data.MenuUbuntu = strings.Contains(version.Script, "item ubuntu Ubuntu Desktop 26.04.1\n")
			data.MenuWindows10 = strings.Contains(version.Script, "item windows10 Windows 10 installer - WinPE\n")
			data.MenuWindows11 = strings.Contains(version.Script, "item windows11 Windows 11 installer - WinPE\n")
			folders := menuFolderPaths(version.Script)
			data.MenuFolderUbuntu = folders["ubuntu"]
			data.MenuFolderWindows10 = folders["windows10"]
			data.MenuFolderWindows11 = folders["windows11"]
			data.HasMenuFolders = data.MenuFolderUbuntu != "" || data.MenuFolderWindows10 != "" || data.MenuFolderWindows11 != ""
			break
		}
	}
	render(w, "menu", data)
}

func (s *Server) menuPublish(w http.ResponseWriter, r *http.Request) {
	title, label, message := r.FormValue("title"), r.FormValue("label"), r.FormValue("message")
	script, err := BuildMenuFromForm(r.Form, s.BootBaseURL)
	if err != nil {
		http.Error(w, "Недопустимый текст меню: "+err.Error(), 400)
		return
	}
	m := MenuVersion{Title: title, Label: label, Message: message, Script: string(script)}
	s.publishMenu(w, r, m, "menu.publish")
}

func (s *Server) menuRollback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверная версия", 400)
		return
	}
	m, err := s.Store.MenuByID(r.Context(), id)
	if err != nil {
		http.Error(w, "Версия не найдена", 404)
		return
	}
	if len(m.Script) == 0 || len(m.Script) > 256<<10 {
		http.Error(w, "Недопустимый снимок меню", 400)
		return
	}
	s.publishMenu(w, r, m, "menu.rollback")
}

func (s *Server) publishMenu(w http.ResponseWriter, r *http.Request, m MenuVersion, action string) {
	s.menuMu.Lock()
	defer s.menuMu.Unlock()
	info := requestInfo(r)
	if s.Boot == nil {
		http.Error(w, "Boot недоступен", 503)
		return
	}
	previous, err := s.Boot.CurrentMenu(r.Context())
	if err != nil {
		http.Error(w, "Не удалось получить текущее меню", 503)
		return
	}
	versions, err := s.Store.MenuVersions(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать версии меню", 503)
		return
	}
	if len(versions) == 0 {
		oldSum := sha256.Sum256(previous)
		initial := MenuVersion{Title: "PXE Platform", Label: "Diagnostics", Message: "PXE Platform network boot is ready", Script: string(previous), SHA256: hex.EncodeToString(oldSum[:])}
		if err := s.Store.SaveActiveMenu(r.Context(), 0, initial); err != nil {
			http.Error(w, "Не удалось сохранить начальное меню", 503)
			return
		}
	}
	sum := sha256.Sum256([]byte(m.Script))
	m.SHA256 = hex.EncodeToString(sum[:])
	hash, err := s.Boot.Publish(r.Context(), []byte(m.Script))
	if err != nil {
		state, checkErr := s.Boot.Status(r.Context())
		if checkErr != nil || state.SHA256 != m.SHA256 {
			_ = s.Store.Audit(r.Context(), info.User.ID, action, "error", "boot publish")
			http.Error(w, "Публикация не подтверждена", 503)
			return
		}
		hash = state.SHA256
	}
	if hash != m.SHA256 {
		http.Error(w, "Контрольная сумма Boot не совпала", 502)
		return
	}
	if err := s.Store.SaveActiveMenu(r.Context(), info.User.ID, m); err != nil {
		_, rollbackErr := s.Boot.Publish(r.Context(), previous)
		_ = s.Store.Audit(r.Context(), info.User.ID, action, "error", "database write")
		if rollbackErr != nil {
			http.Error(w, "Не удалось сохранить версию и вернуть старое меню: "+fmt.Sprint(rollbackErr), 500)
			return
		}
		http.Error(w, "Не удалось сохранить версию; старое меню восстановлено", 500)
		return
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, action, "ok", "sha256="+m.SHA256)
	http.Redirect(w, r, "/menu?message=Меню+опубликовано", 303)
}
