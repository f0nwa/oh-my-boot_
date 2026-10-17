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

	"pxe-platform/internal/agent"
)

const maxWindowsAnswerBytes = 1 << 20
const windowsAnswerNamespace = "urn:schemas-microsoft-com:unattend"

type WindowsAnswerVariant struct {
	ID        int64
	ProfileID int64
	Title     string
	Folder    string
	SHA256    string
	Size      int64
	State     string
	Error     string
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
	if s.WindowsAnswers == nil {
		http.Error(w, "Подготовка сценариев недоступна", http.StatusServiceUnavailable)
		return
	}
	profileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || profileID <= 0 {
		http.Error(w, "Неверный профиль ISO", http.StatusBadRequest)
		return
	}
	profile, err := s.Store.WindowsProfile(r.Context(), profileID)
	if err != nil {
		http.Error(w, "Профиль ISO не найден", http.StatusNotFound)
		return
	}
	if profile.State != "ready" {
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
	sum := sha256.Sum256(data)
	variant, err := s.Store.CreateWindowsAnswer(r.Context(), WindowsAnswerVariant{ProfileID: profileID, Title: title, Folder: folder, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	if err != nil {
		http.Error(w, "Не удалось сохранить сценарий", http.StatusServiceUnavailable)
		return
	}
	request := agent.WindowsAnswerRequest{ID: variant.ID, ProfileID: profileID, SHA256: variant.SHA256, XML: data}
	if err := s.WindowsAnswers.PrepareWindowsAnswer(r.Context(), request); err != nil {
		_ = s.Store.SetWindowsAnswerState(r.Context(), variant.ID, "error", "Не удалось подготовить файл")
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.create", "error", fmt.Sprintf("id=%d", variant.ID))
		http.Error(w, "Не удалось подготовить сценарий", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.SetWindowsAnswerState(r.Context(), variant.ID, "ready", ""); err != nil {
		http.Error(w, "Не удалось сохранить состояние сценария", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.create", "ok", fmt.Sprintf("id=%d sha256=%s", variant.ID, variant.SHA256))
	http.Redirect(w, r, "/images?message=Сценарий+загружен", http.StatusSeeOther)
}
