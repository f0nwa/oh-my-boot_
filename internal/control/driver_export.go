package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"omb/internal/agent"
)

func (s *Server) driverExportAPI() DriverPackageExportAPI {
	if s.DriverExport != nil {
		return s.DriverExport
	}
	if s.Agent != nil {
		return s.Agent
	}
	return nil
}

func driverExportFolderName(pkg DriverPackage) string {
	sha8 := pkg.SHA256
	if len(sha8) > 8 {
		sha8 = sha8[:8]
	}
	raw := strings.TrimSpace(pkg.Title)
	if raw == "" {
		return "driver-" + sha8
	}
	var b strings.Builder
	prevSpace := false
	for _, r := range raw {
		switch {
		case unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('-')
			prevSpace = false
		case unicode.IsSpace(r):
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	cleaned := strings.Trim(b.String(), " .-")
	runes := []rune(cleaned)
	if len(runes) > 64 {
		cleaned = strings.Trim(string(runes[:64]), " .-")
	}
	if cleaned == "" {
		return "driver-" + sha8
	}
	return cleaned + "-" + sha8
}

func isSafeASCIIFilename(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ' ' || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func rfc5987Encode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

func driverExportContentDisposition(folder, sha256 string, multi bool, now time.Time) string {
	if multi {
		return fmt.Sprintf(`attachment; filename="omb-drivers-%s.zip"`, now.Format("2006-01-02"))
	}
	if isSafeASCIIFilename(folder) {
		return fmt.Sprintf(`attachment; filename="%s.zip"`, folder)
	}
	sha8 := sha256
	if len(sha8) > 8 {
		sha8 = sha8[:8]
	}
	return fmt.Sprintf(`attachment; filename="driver-%s.zip"; filename*=UTF-8''%s`, sha8, rfc5987Encode(folder+".zip"))
}

func (s *Server) driverPackageExportOne(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimSpace(r.PathValue("sha"))
	if !validAnswerHash(hash) {
		http.Error(w, "Неверный пакет драйверов", http.StatusBadRequest)
		return
	}
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог драйверов", http.StatusServiceUnavailable)
		return
	}
	for _, pkg := range packages {
		if pkg.SHA256 == hash {
			s.streamDriverPackagesExport(w, r, []DriverPackage{pkg}, false, "sha256="+hash)
			return
		}
	}
	http.Error(w, "Пакет драйверов не найден", http.StatusNotFound)
}

func (s *Server) driverPackagesExportAll(w http.ResponseWriter, r *http.Request) {
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог драйверов", http.StatusServiceUnavailable)
		return
	}
	if len(packages) == 0 {
		http.Error(w, "В каталоге пока нет пакетов драйверов", http.StatusNotFound)
		return
	}
	s.streamDriverPackagesExport(w, r, packages, true, fmt.Sprintf("count=%d mode=all", len(packages)))
}

func (s *Server) driverPackagesExportBatch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректный запрос", http.StatusBadRequest)
		return
	}
	rawItems := r.PostForm["item"]
	if len(rawItems) == 0 {
		http.Error(w, "Не выбрано ни одного пакета", http.StatusBadRequest)
		return
	}
	seen := make(map[string]bool, len(rawItems))
	hashes := make([]string, 0, len(rawItems))
	for _, raw := range rawItems {
		h := strings.TrimSpace(raw)
		if !validAnswerHash(h) {
			http.Error(w, "Неверный пакет драйверов", http.StatusBadRequest)
			return
		}
		if seen[h] {
			continue
		}
		seen[h] = true
		hashes = append(hashes, h)
	}

	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог драйверов", http.StatusServiceUnavailable)
		return
	}
	byHash := make(map[string]DriverPackage, len(packages))
	for _, pkg := range packages {
		byHash[pkg.SHA256] = pkg
	}
	chosen := make([]DriverPackage, 0, len(hashes))
	for _, h := range hashes {
		pkg, ok := byHash[h]
		if !ok {
			http.Error(w, "Пакет драйверов не найден", http.StatusNotFound)
			return
		}
		chosen = append(chosen, pkg)
	}
	multi := len(chosen) > 1
	detail := fmt.Sprintf("count=%d mode=selected", len(chosen))
	if len(chosen) == 1 {
		detail = "sha256=" + chosen[0].SHA256 + " mode=selected"
	}
	s.streamDriverPackagesExport(w, r, chosen, multi, detail)
}

func (s *Server) streamDriverPackagesExport(w http.ResponseWriter, r *http.Request, pkgs []DriverPackage, multi bool, auditDetail string) {
	api := s.driverExportAPI()
	if api == nil {
		http.Error(w, "Экспорт драйверов недоступен", http.StatusServiceUnavailable)
		return
	}
	items := make([]agent.DriverExportItem, 0, len(pkgs))
	for _, pkg := range pkgs {
		items = append(items, agent.DriverExportItem{
			SHA256: pkg.SHA256,
			Folder: driverExportFolderName(pkg),
		})
	}
	userID := requestInfo(r).User.ID
	auditCtx := context.WithoutCancel(r.Context())

	body, err := api.ExportDriverPackages(r.Context(), items)
	if err != nil {
		_ = s.Store.Audit(auditCtx, userID, "driver-package.export", "error", auditDetail)
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Пакет драйверов не найден", http.StatusNotFound)
			return
		}
		http.Error(w, "Не удалось подготовить архив драйверов", http.StatusServiceUnavailable)
		return
	}
	defer body.Close()

	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", driverExportContentDisposition(items[0].Folder, items[0].SHA256, multi, time.Now()))

	outcome := "ok"
	if _, err := io.Copy(w, body); err != nil {
		outcome = "error"
	}
	_ = s.Store.Audit(auditCtx, userID, "driver-package.export", outcome, auditDetail)
}
