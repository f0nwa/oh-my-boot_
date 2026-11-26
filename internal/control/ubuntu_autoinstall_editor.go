package control

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"omb/internal/autoinstall"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type ubuntuAutoinstallEditorView struct {
	Status                                                               int
	Title                                                                string
	Values                                                               autoinstall.Values
	YAML                                                                 string
	Token                                                                string
	File                                                                 *UbuntuAutoinstallFile
	Form                                                                 autoinstall.Form
	HasForm, CanSave, UnknownSource                                      bool
	Section, Tab, Message, PackagesText, DNSText, KeysText, SourceDigest string
	Sections                                                             []ubuntuEditorSection
	Issues                                                               []ubuntuEditorIssue
	Summary                                                              []ubuntuSummaryItem
	Sources                                                              []autoinstall.SourceOption
	Keyboards                                                            []ubuntuKeyboardRow
	ISOProfiles                                                          []BootProfile
	ISOSelected                                                          int64
	EditID                                                               int64
	EditSHA                                                              string
	MenuCount                                                            int
}

func parseUbuntuEditID(r *http.Request) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("edit_id")), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func autoinstallForm(r *http.Request) autoinstall.Form {
	layout := r.FormValue("keyboard")
	variant := r.FormValue("keyboard_variant")
	if rows, ok := r.Form["keyboard_layout"]; ok {
		variants := r.Form["keyboard_layout_variant"]
		layouts := []string{}
		kept := []string{}
		for i, l := range rows {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			layouts = append(layouts, l)
			v := ""
			if i < len(variants) {
				v = strings.TrimSpace(variants[i])
			}
			kept = append(kept, v)
		}
		layout = strings.Join(layouts, ",")
		variant = strings.Join(kept, ",")
	}
	lines := func(name string) []string {
		var out []string
		for _, line := range strings.Split(strings.ReplaceAll(r.FormValue(name), "\r\n", "\n"), "\n") {
			if x := strings.TrimSpace(line); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	return autoinstall.Form{Locale: r.FormValue("locale"), Keyboard: layout, KeyboardVariant: variant, KeyboardToggle: r.FormValue("keyboard_toggle"), TimeZone: r.FormValue("timezone"), Hostname: r.FormValue("hostname"), Username: r.FormValue("username"), Password: r.FormValue("password"), DiskMode: r.FormValue("disk_mode"), DiskSerial: r.FormValue("disk_serial"), RealName: r.FormValue("realname"), SourceID: r.FormValue("source_id"), Drivers: r.FormValue("drivers") == "true", Codecs: r.FormValue("codecs") == "true", Packages: lines("packages"), MirrorMode: r.FormValue("mirror_mode"), MirrorURL: r.FormValue("mirror_url"), Network: autoinstall.NetworkValues{Mode: r.FormValue("network_mode"), MAC: r.FormValue("network_mac"), Address: r.FormValue("network_address"), Gateway: r.FormValue("network_gateway"), DNS: lines("network_dns")}, SSH: autoinstall.SSHValues{InstallServer: r.FormValue("ssh_install") == "true", AllowPassword: r.FormValue("ssh_password") == "true", AuthorizedKeys: lines("ssh_keys")}, PostInstallScript: strings.ReplaceAll(r.FormValue("post_install_script"), "\r\n", "\n"), Shutdown: r.FormValue("shutdown")}
}

func (s *Server) ubuntuAutoinstallNew(w http.ResponseWriter, r *http.Request) {
	s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Values: autoinstall.Values{Locale: "en_US.UTF-8", Keyboard: "us", TimeZone: "UTC", DiskMode: "interactive"}})
}

func (s *Server) renderUbuntuAutoinstall(w http.ResponseWriter, r *http.Request, view *ubuntuAutoinstallEditorView) {
	s.prepareUbuntuView(r, view)
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if view.Status != 0 {
			w.WriteHeader(view.Status)
		}
		_ = json.NewEncoder(w).Encode(ubuntuPreviewResponse{Token: view.Token, YAML: view.YAML, Summary: view.Summary, Issues: view.Issues, Ready: view.CanSave})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if view.Status != 0 {
		w.WriteHeader(view.Status)
	}
	render(w, "ubuntu_autoinstall_editor", pageData{Info: requestInfo(r), UbuntuEditor: view})
}

type ubuntuPreviewResponse struct {
	Token   string              `json:"token"`
	YAML    string              `json:"yaml"`
	Summary []ubuntuSummaryItem `json:"summary"`
	Issues  []ubuntuEditorIssue `json:"issues"`
	Ready   bool                `json:"ready"`
}

//go:embed ubuntu_autoinstall_editor.js
var ubuntuAutoinstallEditorJS []byte

func ubuntuAutoinstallEditorScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(ubuntuAutoinstallEditorJS)
}

func (s *Server) ubuntuAutoinstallPreview(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	editID := parseUbuntuEditID(r)
	if r.FormValue("section") != "" || r.FormValue("tab") != "" {
		view := &ubuntuAutoinstallEditorView{Title: title, Form: autoinstallForm(r), HasForm: true, EditID: editID}
		if payload, ok := verifyAutoinstallPreview(r.FormValue("preview_token"), requestInfo(r).Token); ok {
			view.Token = r.FormValue("preview_token")
			id, digest, sourceErr := s.selectedUbuntuEditorSource(r)
			if sourceErr == nil && payload.ISOID == id && payload.ISOSHA == digest && matchesAutoinstallPreview(payload, title, autoinstallForm(r)) {
				data, _ := autoinstall.Render(payload.Values)
				view.YAML = string(data)
				view.Values = payload.Values
				view.CanSave = true
			}
		}
		s.renderUbuntuAutoinstall(w, r, view)
		return
	}
	if !safeMenuText(title, 80) {
		s.ubuntuFormError(w, r, title, autoinstallForm(r), fmt.Errorf("title: required"))
		return
	}
	form := autoinstallForm(r)
	var values autoinstall.Values
	var data []byte
	var err error
	if form.Password != "" {
		values, data, err = autoinstall.Compile(form)
	} else {
		var savedHash string
		if previous, ok := verifyAutoinstallPreview(r.FormValue("preview_token"), requestInfo(r).Token); ok {
			savedHash = previous.Values.PasswordHash
		} else if editID > 0 {
			if existing, loadErr := s.Store.UbuntuAutoinstall(r.Context(), editID); loadErr == nil && !existing.IsUploaded() {
				savedHash = existing.Values.PasswordHash
			}
		}
		if savedHash == "" {
			err = fmt.Errorf("password: required")
		} else {
			values, err = autoinstall.Normalize(form)
			if err == nil {
				values.PasswordHash = savedHash
				data, err = autoinstall.Render(values)
			}
		}
	}
	isoID, isoSHA, sourceErr := s.selectedUbuntuEditorSource(r)
	if err == nil && sourceErr != nil {
		err = sourceErr
	}
	if err == nil {
		err = s.validateUbuntuEditorSource(r.Context(), values, isoID, isoSHA)
	}
	if err != nil {
		s.ubuntuFormError(w, r, title, form, err)
		return
	}

	payload := autoinstallPreviewPayload{Title: title, Values: values, Digest: fmt.Sprintf("%x", sha256.Sum256(data)), Expires: time.Now().Add(autoinstallPreviewLifetime).Unix(), ISOID: isoID, ISOSHA: isoSHA}
	token, err := signAutoinstallPreview(payload, requestInfo(r).Token)
	if err != nil {
		http.Error(w, "Не удалось создать предпросмотр Autoinstall", http.StatusServiceUnavailable)
		return
	}
	s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Title: title, Values: values, YAML: string(data), Token: token, CanSave: true, EditID: editID})
}

func downloadAutoinstall(w http.ResponseWriter, data []byte) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="user-data"`)
	_, _ = w.Write(data)
}

func (s *Server) ubuntuAutoinstallPreviewDownload(w http.ResponseWriter, r *http.Request) {
	payload, ok := verifyAutoinstallPreview(r.FormValue("preview_token"), requestInfo(r).Token)
	if !ok {
		http.Error(w, "Предпросмотр истёк или поля изменились. Создайте новый предпросмотр.", http.StatusBadRequest)
		return
	}
	data, _ := autoinstall.Render(payload.Values)
	downloadAutoinstall(w, data)
}

func (s *Server) ubuntuAutoinstallSave(w http.ResponseWriter, r *http.Request) {
	payload, ok := verifyAutoinstallPreview(r.FormValue("preview_token"), requestInfo(r).Token)
	isoID, isoSHA, sourceErr := s.selectedUbuntuEditorSource(r)
	if !ok || sourceErr != nil || payload.ISOID != isoID || payload.ISOSHA != isoSHA || !matchesAutoinstallPreview(payload, r.FormValue("title"), autoinstallForm(r)) {
		s.ubuntuFormError(w, r, r.FormValue("title"), autoinstallForm(r), fmt.Errorf("password: preview expired or fields changed"))
		return
	}
	if err := s.validateUbuntuEditorSource(r.Context(), payload.Values, isoID, isoSHA); err != nil {
		s.ubuntuFormError(w, r, r.FormValue("title"), autoinstallForm(r), fmt.Errorf("source: selected source unavailable"))
		return
	}
	data, err := autoinstall.Render(payload.Values)
	if err != nil {
		http.Error(w, "Предпросмотр недействителен", http.StatusBadRequest)
		return
	}
	editID := parseUbuntuEditID(r)
	saveMode := r.FormValue("save_mode")
	if editID > 0 && saveMode == "update" {
		updated, err := s.Store.UpdateUbuntuAutoinstall(r.Context(), UbuntuAutoinstallFile{
			ID:         editID,
			Title:      payload.Title,
			SourceKind: "editor",
			Values:     payload.Values,
			UserData:   data,
			SHA256:     payload.Digest,
			State:      "validated",
		})
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				http.Error(w, "Такой скрипт Ubuntu Autoinstall уже загружен", http.StatusConflict)
				return
			}
			http.Error(w, "Не удалось обновить файл Autoinstall", http.StatusServiceUnavailable)
			return
		}
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.update", "ok", strconv.FormatInt(updated.ID, 10))
		msg := fmt.Sprintf("Скрипт #%d «%s» обновлён", updated.ID, updated.Title)
		http.Redirect(w, r, "/answers?message="+url.QueryEscape(msg)+"#ubuntu-autoinstall", http.StatusSeeOther)
		return
	}
	file, err := s.Store.CreateUbuntuAutoinstall(r.Context(), UbuntuAutoinstallFile{Title: payload.Title, SourceKind: "editor", Values: payload.Values, UserData: data, SHA256: payload.Digest, State: "validated"})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "Такой скрипт Ubuntu Autoinstall уже загружен", http.StatusConflict)
			return
		}
		http.Error(w, "Не удалось сохранить файл Autoinstall", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.create", "ok", strconv.FormatInt(file.ID, 10))
	s.InvalidateResourceAuthorsCache()
	http.Redirect(w, r, "/ubuntu-autoinstall/"+strconv.FormatInt(file.ID, 10), http.StatusSeeOther)
}

func (s *Server) ubuntuAutoinstallFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	file, err := s.Store.UbuntuAutoinstall(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/download"):
		downloadAutoinstall(w, file.UserData)
	case strings.HasSuffix(r.URL.Path, "/copy"):
		if file.IsUploaded() {
			http.NotFound(w, r)
			return
		}
		values := file.Values
		values.PasswordHash = ""
		s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Title: file.Title, Values: values})
	default:
		if !file.IsUploaded() && Allowed(requestInfo(r).User.Role, "images") {
			payload := autoinstallPreviewPayload{
				Title:   file.Title,
				Values:  file.Values,
				Digest:  file.SHA256,
				Expires: time.Now().Add(autoinstallPreviewLifetime).Unix(),
			}
			token, _ := signAutoinstallPreview(payload, requestInfo(r).Token)
			s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{
				Title:   file.Title,
				Values:  file.Values,
				YAML:    string(file.UserData),
				Token:   token,
				CanSave: token != "",
				EditID:  file.ID,
				EditSHA: file.SHA256,
			})
			return
		}
		s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Title: file.Title, Values: file.Values, YAML: string(file.UserData), File: &file})
	}
}
