package control

import (
	"archive/zip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"omb/internal/agent"
	"omb/internal/drivers"
)

//go:embed driver_upload.js
var driverUploadJS []byte

func (s *Server) driverUploadScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(driverUploadJS)
}

func (s *Server) driversPage(w http.ResponseWriter, r *http.Request) {
	packages, err := s.Store.DriverPackages(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать каталог драйверов", http.StatusServiceUnavailable)
		return
	}
	authors := s.resourceAuthors(r.Context())
	for i := range packages {
		packages[i].Author = authors.Drivers[packages[i].SHA256]
	}
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		http.Error(w, "Профили ISO недоступны", http.StatusServiceUnavailable)
		return
	}
	configs, err := s.Store.DriverConfigs(r.Context())
	if err != nil {
		http.Error(w, "Конфигурации установки недоступны", http.StatusServiceUnavailable)
		return
	}
	usage := s.loadDriverUsage(r.Context(), profiles, configs)
	var diskReport *agent.DiskUsageReport
	diskAPI := s.DiskUsage
	if diskAPI == nil && s.Agent != nil {
		diskAPI = s.Agent
	}
	if diskAPI != nil {
		if rep, err := diskAPI.DiskUsage(r.Context()); err == nil {
			diskReport = &rep
		}
	}
	populateDriverConfigSizes(configs, diskReport)
	for i := range configs {
		if configs[i].Author != "" {
			continue
		}
		if direct := authors.DriverConfigs[configs[i].ID]; direct != "" {
			configs[i].Author = direct
			configs[i].AuthorInferred = false
			continue
		}
		for _, use := range usage.Configs[configs[i].ID] {
			if inferred := authors.Installations[use.ID]; inferred != "" {
				configs[i].Author = inferred
				configs[i].AuthorInferred = true
				break
			}
		}
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	sortBy := r.URL.Query().Get("sort")
	dir := r.URL.Query().Get("dir")
	if query != "" {
		configs = filterDriverConfigs(configs, usage, query)
	}
	if sortBy != "" {
		sortDriverConfigs(configs, usage, sortBy, dir)
	}
	render(w, "drivers", pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), Error: pageError(r), DriverPackages: packages, WindowsProfiles: profiles, DriverConfigs: configs, DriverUsage: usage, Query: query, Sort: sortBy, Dir: dir})
}

func filterDriverConfigs(configs []DriverConfig, usage driverUsage, query string) []DriverConfig {
	q := strings.ToLower(query)
	var filtered []DriverConfig
	for _, cfg := range configs {
		var parts []string
		parts = append(parts, cfg.Title)
		parts = append(parts, fmt.Sprintf("№ %d", cfg.ID))
		parts = append(parts, strconv.FormatInt(cfg.ID, 10))
		parts = append(parts, cfg.Author)
		parts = append(parts, cfg.State)
		parts = append(parts, cfg.Error)
		parts = append(parts, usage.Profiles[cfg.ProfileID])
		parts = append(parts, usage.ProfilePaths[cfg.ProfileID])
		parts = append(parts, usage.Answers[cfg.AnswerID])
		if cfg.Build.WIMSHA256 != "" {
			parts = append(parts, cfg.Build.WIMSHA256)
		}
		for _, inf := range cfg.PackageINFs {
			parts = append(parts, inf.INFPath, inf.PackageSHA256)
		}
		for _, use := range usage.Configs[cfg.ID] {
			parts = append(parts, use.Title, fmt.Sprintf("№ %d", use.ID))
		}
		haystack := strings.ToLower(strings.Join(parts, " "))
		if strings.Contains(haystack, q) || searchMatchSHA(q, cfg.Build.WIMSHA256) {
			filtered = append(filtered, cfg)
			continue
		}
		match := false
		for _, inf := range cfg.PackageINFs {
			if searchMatchSHA(q, inf.PackageSHA256) {
				match = true
				break
			}
		}
		if match {
			filtered = append(filtered, cfg)
		}
	}
	return filtered
}

func sortDriverConfigs(configs []DriverConfig, usage driverUsage, sortBy, dir string) {
	desc := dir == "desc"
	titleOf := func(c DriverConfig) string {
		if c.Title != "" {
			return c.Title
		}
		return fmt.Sprintf("№ %d", c.ID)
	}
	switch sortBy {
	case "config":
		sort.SliceStable(configs, func(i, j int) bool {
			if c := compareStrings(titleOf(configs[i]), titleOf(configs[j]), desc); c != 0 {
				return c < 0
			}
			return configs[i].ID < configs[j].ID
		})
	case "author":
		sort.SliceStable(configs, func(i, j int) bool {
			if c := compareStrings(configs[i].Author, configs[j].Author, desc); c != 0 {
				return c < 0
			}
			return compareTimes(configs[i].CreatedAt, configs[j].CreatedAt, desc) < 0
		})
	case "size":
		sort.SliceStable(configs, func(i, j int) bool {
			if c := compareInt64s(configs[i].SizeBytes, configs[j].SizeBytes, desc); c != 0 {
				return c < 0
			}
			return configs[i].ID < configs[j].ID
		})
	case "uses":
		sort.SliceStable(configs, func(i, j int) bool {
			uI := len(usage.Configs[configs[i].ID])
			uJ := len(usage.Configs[configs[j].ID])
			if c := compareInts(uI, uJ, desc); c != 0 {
				return c < 0
			}
			return configs[i].ID < configs[j].ID
		})
	}
}

func normalizeDriverPackageTitle(raw string) (string, bool) {
	title := strings.TrimSpace(raw)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) > 100 {
		return "", false
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return title, true
}

func normalizeDriverPackageDescription(raw string) (string, bool) {
	desc := strings.ReplaceAll(raw, "\r\n", "\n")
	desc = strings.ReplaceAll(desc, "\r", "\n")
	desc = strings.TrimSpace(desc)
	if !utf8.ValidString(desc) || utf8.RuneCountInString(desc) > 400 {
		return "", false
	}
	for _, r := range desc {
		if unicode.IsControl(r) && r != '\n' {
			return "", false
		}
	}
	return desc, true
}

func (s *Server) driverPackageUpdateMetadata(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimSpace(r.PathValue("sha"))
	if !validAnswerHash(hash) {
		http.Error(w, "Неверный пакет драйверов", http.StatusBadRequest)
		return
	}
	title, ok := normalizeDriverPackageTitle(r.FormValue("title"))
	if !ok {
		http.Error(w, "Название пакета: до 100 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	desc, ok := normalizeDriverPackageDescription(r.FormValue("description"))
	if !ok {
		http.Error(w, "Описание пакета: до 400 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	if err := s.Store.UpdateDriverPackageMetadata(r.Context(), hash, title, desc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Пакет драйверов не найден", http.StatusNotFound)
			return
		}
		http.Error(w, "Не удалось сохранить свойства пакета драйверов", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver-package.update", "ok", fmt.Sprintf("sha256=%s title=%s", hash, title))
	http.Redirect(w, r, "/drivers?message="+url.QueryEscape("Свойства пакета драйверов обновлены"), http.StatusSeeOther)
}

func (s *Server) driversImport(w http.ResponseWriter, r *http.Request) {
	if s.DriverPackages == nil || r.MultipartForm == nil {
		http.Error(w, "Импорт драйверов недоступен", http.StatusServiceUnavailable)
		return
	}
	defer r.MultipartForm.RemoveAll()
	var rawTitle, rawDesc string
	if vals := r.MultipartForm.Value["title"]; len(vals) > 0 {
		rawTitle = vals[0]
	}
	if vals := r.MultipartForm.Value["description"]; len(vals) > 0 {
		rawDesc = vals[0]
	}
	title, ok := normalizeDriverPackageTitle(rawTitle)
	if !ok {
		http.Error(w, "Название пакета: до 100 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	desc, ok := normalizeDriverPackageDescription(rawDesc)
	if !ok {
		http.Error(w, "Описание пакета: до 400 символов без управляющих знаков", http.StatusBadRequest)
		return
	}
	archive, size, err := spoolDriverUpload(r)
	if err != nil {
		http.Error(w, "Некорректный пакет: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "Не удалось прочитать пакет", http.StatusInternalServerError)
		return
	}
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	inventory, err := s.DriverPackages.ImportDriverPackage(r.Context(), archive, size)
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "error", "agent")
		http.Error(w, "Не удалось проверить пакет драйвера: "+err.Error(), http.StatusBadRequest)
		return
	}
	created, err := s.Store.CreateDriverPackage(r.Context(), inventory, title, desc)
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "error", "database")
		http.Error(w, "Не удалось добавить пакет в каталог", http.StatusServiceUnavailable)
		return
	}
	pkgName := title
	if pkgName == "" && len(r.MultipartForm.File["zip"]) > 0 && r.MultipartForm.File["zip"][0].Filename != "" {
		pkgName = r.MultipartForm.File["zip"][0].Filename
	}
	if pkgName == "" && len(inventory.SHA256) >= 8 {
		pkgName = inventory.SHA256[:8]
	}
	if !created {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "ok", inventory.SHA256+" existing")
		msg := "Пакет уже есть в каталоге"
		if pkgName != "" {
			msg = fmt.Sprintf("Пакет «%s» уже есть в каталоге", pkgName)
		}
		http.Redirect(w, r, "/drivers?message="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "ok", inventory.SHA256)
	s.InvalidateResourceAuthorsCache()
	msg := "Пакет драйвера добавлен"
	if pkgName != "" {
		msg = fmt.Sprintf("Пакет драйверов «%s» добавлен", pkgName)
	}
	http.Redirect(w, r, "/drivers?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func spoolDriverUpload(r *http.Request) (*os.File, int64, error) {
	archive, err := os.CreateTemp("", "omb-driver-upload-*.zip")
	if err != nil {
		return nil, 0, err
	}
	if err := archive.Chmod(0600); err != nil {
		archive.Close()
		os.Remove(archive.Name())
		return nil, 0, err
	}
	clean := func(err error) (*os.File, int64, error) {
		archive.Close()
		os.Remove(archive.Name())
		return nil, 0, err
	}
	zips := r.MultipartForm.File["zip"]
	folders := r.MultipartForm.File["files[]"]
	if len(zips) == 1 && len(folders) == 0 {
		if !strings.HasSuffix(strings.ToLower(zips[0].Filename), ".zip") {
			return clean(fmt.Errorf("выберите ZIP-файл"))
		}
		input, err := zips[0].Open()
		if err != nil {
			return clean(err)
		}
		written, copyErr := io.Copy(archive, io.LimitReader(input, drivers.MaxArchiveBytes+1))
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil {
			return clean(fmt.Errorf("не удалось прочитать ZIP"))
		}
		if written <= 0 || written > drivers.MaxArchiveBytes {
			return clean(fmt.Errorf("ZIP превышает 256 МиБ"))
		}
		return archive, written, nil
	}
	paths := r.MultipartForm.Value["paths[]"]
	if len(zips) != 0 || len(folders) == 0 || len(folders) != len(paths) || len(folders) > drivers.MaxFiles {
		return clean(fmt.Errorf("выберите одну папку или один ZIP"))
	}
	zw := zip.NewWriter(archive)
	var total int64
	seen := map[string]bool{}
	for i, header := range folders {
		name := paths[i]
		if err := drivers.ValidateUploadPath(name); err != nil {
			zw.Close()
			return clean(err)
		}
		if seen[strings.ToLower(name)] {
			zw.Close()
			return clean(fmt.Errorf("повторяющийся путь: %s", name))
		}
		seen[strings.ToLower(name)] = true
		if header.Size < 0 || header.Size > drivers.MaxArchiveBytes-total {
			zw.Close()
			return clean(fmt.Errorf("папка превышает 256 МиБ"))
		}
		total += header.Size
		input, err := header.Open()
		if err != nil {
			zw.Close()
			return clean(err)
		}
		out, err := zw.Create(name)
		if err != nil {
			input.Close()
			zw.Close()
			return clean(err)
		}
		written, copyErr := io.Copy(out, input)
		closeErr := input.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			zw.Close()
			return clean(fmt.Errorf("не удалось прочитать файл %s", name))
		}
	}
	if err := zw.Close(); err != nil {
		return clean(err)
	}
	size, err := archive.Seek(0, io.SeekCurrent)
	if err != nil {
		return clean(err)
	}
	if size > drivers.MaxArchiveBytes {
		return clean(fmt.Errorf("пакет превышает 256 МиБ"))
	}
	return archive, size, nil
}
