package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"omb/internal/agent"
)

const maxWindowsAnswerBytes = 1 << 20
const windowsAnswerNamespace = "urn:schemas-microsoft-com:unattend"

type WindowsAnswerVariant struct {
	ID        int64
	ProfileID int64
	Title     string
	Author    string
	Folder    string
	SHA256    string
	Size      int64
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
		http.Error(w, "Подготовка сценариев недоступна", http.StatusServiceUnavailable)
		return
	}
	rawProfile := r.PathValue("id")
	if rawProfile == "" {
		rawProfile = r.FormValue("profile_id")
	}
	profileID, err := strconv.ParseInt(rawProfile, 10, 64)
	if err != nil || profileID <= 0 {
		http.Error(w, "Неверный профиль ISO", http.StatusBadRequest)
		return
	}
	profile, err := s.Store.WindowsProfile(r.Context(), profileID)
	if err != nil {
		http.Error(w, "Профиль ISO не найден", http.StatusNotFound)
		return
	}
	if profile.State != "ready" || profile.BootMode != "" {
		http.Error(w, "Сначала подготовьте профиль ISO", http.StatusConflict)
		return
	}
	title, folder := r.FormValue("title"), r.FormValue("folder")
	if !safeMenuText(title, 80) || !isASCII(title) || validateMenuFolder(folder) != nil || !isASCII(folder) {
		http.Error(w, "Название и каталог должны быть латиницей", http.StatusBadRequest)
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
		http.Error(w, "Некорректный XML-файл сценария", http.StatusBadRequest)
		return
	}
	if _, status, message := s.saveWindowsAnswer(r, profileID, title, folder, data, false); status != 0 {
		http.Error(w, message, status)
		return
	}
	http.Redirect(w, r, "/answers?message=Сценарий+загружен", http.StatusSeeOther)
}

// saveWindowsAnswer stores a checked answer file as a new variant of a
// profile and hands it to the Agent; the upload form and the editor use it.
// The caller holds windowsMu and has checked the profile, title and folder.
// A non-zero status comes with the message to show.
func (s *Server) saveWindowsAnswer(r *http.Request, profileID int64, title, folder string, data []byte, editor bool) (WindowsAnswerVariant, int, string) {
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	existing, err := s.Store.WindowsAnswers(r.Context())
	if err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось проверить сценарии этого ISO"
	}
	for _, answer := range existing {
		if answer.ProfileID == profileID && answer.SHA256 == digest {
			return WindowsAnswerVariant{}, http.StatusConflict, fmt.Sprintf("Такой XML уже загружен для этого ISO: сценарий #%d «%s». Каталог в меню можно изменить при публикации.", answer.ID, answer.Title)
		}
	}
	var wipesDisk *bool
	if wipes, known := AnswerWipesDisk(data); known {
		wipesDisk = &wipes
	}
	variant, err := s.Store.CreateWindowsAnswer(r.Context(), WindowsAnswerVariant{ProfileID: profileID, Title: title, Folder: folder, SHA256: digest, Size: int64(len(data)), WipesDisk: wipesDisk})
	if err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось сохранить сценарий"
	}
	request := agent.WindowsAnswerRequest{ID: variant.ID, ProfileID: profileID, SHA256: variant.SHA256, XML: data}
	if err := s.WindowsAnswers.PrepareWindowsAnswer(r.Context(), request); err != nil {
		_ = s.Store.SetWindowsAnswerState(r.Context(), variant.ID, "error", "Не удалось подготовить файл")
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.create", "error", fmt.Sprintf("id=%d", variant.ID))
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось подготовить сценарий"
	}
	if err := s.Store.SetWindowsAnswerState(r.Context(), variant.ID, "ready", ""); err != nil {
		return WindowsAnswerVariant{}, http.StatusServiceUnavailable, "Не удалось сохранить состояние сценария"
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
