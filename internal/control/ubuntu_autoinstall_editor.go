package control

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openwall/yescrypt-go"
	"omb/internal/autoinstall"
)

const autoinstallPreviewLifetime = 10 * time.Minute
const maxAutoinstallPreviewToken = 12 << 10

// The only password representation in a preview is a salted hash. The title is
// bound too, so every constructor field must be resubmitted unchanged on save.
type autoinstallPreviewPayload struct {
	Title   string             `json:"title"`
	Values  autoinstall.Values `json:"values"`
	YAML    []byte             `json:"yaml"`
	Digest  string             `json:"digest"`
	Expires int64              `json:"expires"`
}

type ubuntuAutoinstallEditorView struct {
	Title  string
	Values autoinstall.Values
	YAML   string
	Token  string
	File   *UbuntuAutoinstallFile
}

func autoinstallForm(r *http.Request) autoinstall.Form {
	return autoinstall.Form{Locale: r.FormValue("locale"), Keyboard: r.FormValue("keyboard"), TimeZone: r.FormValue("timezone"), Hostname: r.FormValue("hostname"), Username: r.FormValue("username"), Password: r.FormValue("password"), DiskMode: r.FormValue("disk_mode"), DiskSerial: r.FormValue("disk_serial")}
}

func (s *Server) ubuntuAutoinstallNew(w http.ResponseWriter, r *http.Request) {
	s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Values: autoinstall.Values{Locale: "en_US.UTF-8", Keyboard: "us", TimeZone: "UTC", DiskMode: "interactive"}})
}

func (s *Server) renderUbuntuAutoinstall(w http.ResponseWriter, r *http.Request, view *ubuntuAutoinstallEditorView) {
	w.Header().Set("Cache-Control", "no-store")
	render(w, "ubuntu_autoinstall_editor", pageData{Info: requestInfo(r), UbuntuEditor: view})
}

func (s *Server) ubuntuAutoinstallPreview(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	if !safeMenuText(title, 80) {
		http.Error(w, "Название файла обязательно, до 80 символов", http.StatusBadRequest)
		return
	}
	values, data, err := autoinstall.Compile(autoinstallForm(r))
	if err != nil {
		// Generator errors contain field names and fixed diagnostics, never input.
		field, _, _ := strings.Cut(err.Error(), ":")
		http.Error(w, fmt.Sprintf("Недопустимое значение поля %s", field), http.StatusBadRequest)
		return
	}
	payload := autoinstallPreviewPayload{Title: title, Values: values, YAML: data, Digest: fmt.Sprintf("%x", sha256.Sum256(data)), Expires: time.Now().Add(autoinstallPreviewLifetime).Unix()}
	token, err := signAutoinstallPreview(payload, requestInfo(r).Token)
	if err != nil {
		http.Error(w, "Не удалось создать предпросмотр Autoinstall", http.StatusServiceUnavailable)
		return
	}
	s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Title: title, Values: values, YAML: string(data), Token: token})
}

func signAutoinstallPreview(payload autoinstallPreviewPayload, session string) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(session))
	_, _ = mac.Write(data)
	token := base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > maxAutoinstallPreviewToken {
		return "", fmt.Errorf("preview exceeds size limit")
	}
	return token, nil
}

func verifyAutoinstallPreview(token, session string) (autoinstallPreviewPayload, bool) {
	var payload autoinstallPreviewPayload
	if len(token) > maxAutoinstallPreviewToken {
		return payload, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return payload, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return payload, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return payload, false
	}
	mac := hmac.New(sha256.New, []byte(session))
	_, _ = mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) || json.Unmarshal(data, &payload) != nil {
		return payload, false
	}
	now := time.Now().Unix()
	if payload.Expires <= now || payload.Expires > now+int64(autoinstallPreviewLifetime/time.Second) || !safeMenuText(payload.Title, 80) {
		return payload, false
	}
	rendered, err := autoinstall.Render(payload.Values)
	if err != nil || !bytes.Equal(rendered, payload.YAML) || fmt.Sprintf("%x", sha256.Sum256(rendered)) != payload.Digest {
		return payload, false
	}
	return payload, true
}

func matchesAutoinstallPreview(payload autoinstallPreviewPayload, title string, form autoinstall.Form) bool {
	// Match Compile's normalization without computing a second randomly salted hash.
	values := autoinstall.Values{Locale: strings.TrimSpace(form.Locale), Keyboard: strings.TrimSpace(form.Keyboard), TimeZone: strings.TrimSpace(form.TimeZone), Hostname: strings.TrimSpace(form.Hostname), Username: strings.TrimSpace(form.Username), DiskMode: strings.TrimSpace(form.DiskMode), DiskSerial: form.DiskSerial, PasswordHash: payload.Values.PasswordHash}
	if strings.TrimSpace(title) != payload.Title || values != payload.Values || len(form.Password) == 0 || len(form.Password) > 1024 {
		return false
	}
	hash, err := yescrypt.Hash([]byte(form.Password), []byte(payload.Values.PasswordHash))
	return err == nil && hmac.Equal(hash, []byte(payload.Values.PasswordHash))
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
	downloadAutoinstall(w, payload.YAML)
}

func (s *Server) ubuntuAutoinstallSave(w http.ResponseWriter, r *http.Request) {
	payload, ok := verifyAutoinstallPreview(r.FormValue("preview_token"), requestInfo(r).Token)
	if !ok || !matchesAutoinstallPreview(payload, r.FormValue("title"), autoinstallForm(r)) {
		http.Error(w, "Предпросмотр истёк или поля изменились. Создайте новый предпросмотр.", http.StatusBadRequest)
		return
	}
	file, err := s.Store.CreateUbuntuAutoinstall(r.Context(), UbuntuAutoinstallFile{Title: payload.Title, Values: payload.Values, UserData: payload.YAML, SHA256: payload.Digest, State: "validated"})
	if err != nil {
		http.Error(w, "Не удалось сохранить файл Autoinstall", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.create", "ok", strconv.FormatInt(file.ID, 10))
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
		values := file.Values
		values.PasswordHash = ""
		s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Title: file.Title, Values: values})
	default:
		s.renderUbuntuAutoinstall(w, r, &ubuntuAutoinstallEditorView{Title: file.Title, Values: file.Values, YAML: string(file.UserData), File: &file})
	}
}
