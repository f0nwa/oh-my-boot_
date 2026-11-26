package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"omb/internal/agent"
)

const maxWindowsAnswerBytes = 1 << 20
const windowsAnswerNamespace = "urn:schemas-microsoft-com:unattend"

type WindowsAnswerVariant struct {
	ID        int64
	Title     string
	Author    string
	Folder    string
	SHA256    string
	Size      int64
	CreatedAt time.Time
	Disk      AnswerFileDisk
	Target    WindowsAnswerTarget
	State     string
	Error     string
	// WipesDisk is nil for files uploaded before the check existed.
	WipesDisk *bool
}

// ValidateWindowsAnswer checks transport-level XML safety, not Windows Setup semantics.
func ValidateWindowsAnswer(data []byte) error {
	if len(data) == 0 || len(data) > maxWindowsAnswerBytes || !utf8.Valid(data) {
		return fmt.Errorf("invalid answer file size or encoding")
	}
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		data = data[3:]
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid answer file XML")
		}
		switch t := token.(type) {
		case xml.Directive:
			return fmt.Errorf("XML directives are not allowed")
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots != 1 || t.Name.Local != "unattend" || t.Name.Space != windowsAnswerNamespace {
					return fmt.Errorf("expected Windows unattend root element")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) > 0 {
				return fmt.Errorf("text outside answer file root")
			}
		}
	}
	if roots != 1 || depth != 0 {
		return fmt.Errorf("answer file root is missing")
	}
	return nil
}

func (s *Server) windowsAnswerUpload(w http.ResponseWriter, r *http.Request) {
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.WindowsAnswers == nil {
		http.Error(w, "Подготовка скриптов недоступна", http.StatusServiceUnavailable)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if !safeMenuText(title, 80) {
		http.Error(w, "Укажите название скрипта (до 80 символов)", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Выберите XML-файл", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxWindowsAnswerBytes+1))
	if err != nil || ValidateWindowsAnswer(data) != nil {
		http.Error(w, "Некорректный XML-файл скрипта", http.StatusBadRequest)
		return
	}
	if _, status, message := s.saveWindowsAnswer(r, title, "", data, false); status != 0 {
		http.Error(w, message, status)
		return
	}
	msg := "Скрипт загружен"
	if title != "" {
		msg = fmt.Sprintf("Скрипт «%s» загружен", title)
	}
	http.Redirect(w, r, "/answers?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

// saveWindowsAnswer stores a checked answer file as a standalone variant
// and hands it to the Agent; the upload form and the editor use it.
// The caller holds windowsMu and has checked title and folder.
// A non-zero status comes with the message to show.
func (s *Server) saveWindowsAnswer(r *http.Request, title, folder string, data []byte, editor bool) (WindowsAnswerVariant, int, string) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	existing, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось проверить скрипты"
	}
	for _, answer := range existing {
		if answer.SHA256 == digest {
			return WindowsAnswerVariant{}, http.StatusConflict, fmt.Sprintf("Такой XML уже загружен: скрипт #%d «%s». Каталог в меню можно изменить при публикации.", answer.ID, answer.Title)
		}
	}
	var wipesDisk *bool
	if wipes, known := AnswerWipesDisk(data); known {
		wipesDisk = &wipes
	}
	variant, err := s.Store.CreateWindowsAnswer(r.Context(), WindowsAnswerVariant{Title: title, Folder: folder, SHA256: digest, Size: int64(len(data)), WipesDisk: wipesDisk, Disk: SummarizeWindowsAnswerDisk(data), Target: SummarizeWindowsAnswerTarget(data)})
	if err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось сохранить скрипт"
	}
	request := agent.WindowsAnswerRequest{ID: variant.ID, SHA256: variant.SHA256, XML: data}
	if err := s.WindowsAnswers.PrepareWindowsAnswer(r.Context(), request); err != nil {
		_ = s.Store.SetWindowsAnswerState(r.Context(), variant.ID, "error", "Не удалось подготовить файл")
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.create", "error", fmt.Sprintf("id=%d", variant.ID))
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось подготовить скрипт"
	}
	if err := s.Store.SetWindowsAnswerState(r.Context(), variant.ID, "ready", ""); err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось сохранить состояние скрипта"
	}
	detail := fmt.Sprintf("id=%d sha256=%s", variant.ID, variant.SHA256)
	if editor {
		detail += " editor=1"
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.create", "ok", detail)
	s.InvalidateResourceAuthorsCache()
	variant.State = "ready"
	return variant, 0, ""
}

// updateWindowsAnswer stages a new content-addressed XML version for an existing
// answer ID and updates its database record. Previously staged SHA-256 versions
// remain untouched so active published menus keep working until republished.
func (s *Server) updateWindowsAnswer(r *http.Request, id int64, title string, data []byte) (WindowsAnswerVariant, int, string) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	existing, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось проверить скрипты"
	}
	found := false
	for _, answer := range existing {
		if answer.ID == id {
			found = true
			continue
		}
		if answer.SHA256 == digest {
			return WindowsAnswerVariant{}, http.StatusConflict, fmt.Sprintf("Такой XML уже загружен: скрипт #%d «%s».", answer.ID, answer.Title)
		}
	}
	if !found {
		return WindowsAnswerVariant{}, http.StatusNotFound, "Скрипт не найден"
	}
	request := agent.WindowsAnswerRequest{ID: id, SHA256: digest, XML: data}
	if err := s.WindowsAnswers.PrepareWindowsAnswer(r.Context(), request); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.update", "error", fmt.Sprintf("id=%d", id))
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось подготовить скрипт"
	}
	var wipesDisk *bool
	if wipes, known := AnswerWipesDisk(data); known {
		wipesDisk = &wipes
	}
	variant, err := s.Store.UpdateWindowsAnswer(r.Context(), WindowsAnswerVariant{
		ID:        id,
		Title:     title,
		SHA256:    digest,
		Size:      int64(len(data)),
		WipesDisk: wipesDisk,
		Disk:      SummarizeWindowsAnswerDisk(data),
		Target:    SummarizeWindowsAnswerTarget(data),
	})
	if err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось обновить скрипт"
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.update", "ok", fmt.Sprintf("id=%d sha256=%s editor=1", variant.ID, variant.SHA256))
	return variant, 0, ""
}
