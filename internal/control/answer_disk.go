package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// AnswerWipesDisk reports whether an answer file writes to disks without
// asking: a DiskConfiguration with WillWipeDisk=true or CreatePartition
// entries, a fixed InstallTo target, or a command that runs a diskpart
// script ("diskpart /s file" or "diskpart < file"; a mere mention, such as
// "use diskpart" in a message or "list disk | diskpart", does not count).
// It is a hint for people choosing the file, not a safety barrier. known is
// false when the XML could not be read to the end, for example in UTF-16.
func AnswerWipesDisk(data []byte) (wipes, known bool) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	inDisk := 0
	var path []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return false, true
		}
		if err != nil {
			return false, false
		}
		switch t := token.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			if t.Name.Local == "DiskConfiguration" {
				inDisk++
			}
			if t.Name.Local == "InstallTo" || (inDisk > 0 && (t.Name.Local == "CreatePartition" || t.Name.Local == "CreatePartitions")) {
				return true, true
			}
		case xml.EndElement:
			if t.Name.Local == "DiskConfiguration" && inDisk > 0 {
				inDisk--
			}
			if len(path) > 0 {
				path = path[:len(path)-1]
			}
		case xml.CharData:
			if len(path) > 0 && (path[len(path)-1] == "Path" || path[len(path)-1] == "CommandLine") && diskpartScript.MatchString(string(t)) {
				return true, true
			}
			if inDisk > 0 && len(path) > 0 && path[len(path)-1] == "WillWipeDisk" && strings.EqualFold(strings.TrimSpace(string(t)), "true") {
				return true, true
			}
		}
	}
}

var diskpartScript = regexp.MustCompile(`(?i)diskpart(\.exe)?"?\s*(/s\s|<)`)

// windowsAnswerInspect records the disk flag for an answer file uploaded before
// the flag existed. The file must be the same one, checked by SHA-256.
func (s *Server) windowsAnswerInspect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный сценарий", http.StatusBadRequest)
		return
	}
	answer, err := s.Store.WindowsAnswer(r.Context(), id)
	if err != nil {
		http.Error(w, "Сценарий не найден", http.StatusNotFound)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Выберите XML-файл", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxWindowsAnswerBytes+1))
	if err != nil || int64(len(data)) > maxWindowsAnswerBytes {
		http.Error(w, "Некорректный XML-файл сценария", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != answer.SHA256 {
		http.Error(w, "Это другой файл: контрольная сумма не совпадает", http.StatusConflict)
		return
	}
	wipes, known := AnswerWipesDisk(data)
	if !known {
		http.Error(w, "Не удалось разобрать XML: метка останется «не проверено»", http.StatusUnprocessableEntity)
		return
	}
	if err := s.Store.SetWindowsAnswerWipesDisk(r.Context(), id, wipes); err != nil {
		http.Error(w, "Не удалось сохранить проверку", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "windows-answer.inspect", "ok", fmt.Sprintf("id=%d wipes_disk=%t", id, wipes))
	http.Redirect(w, r, "/answers?message=Файл+проверен", http.StatusSeeOther)
}
