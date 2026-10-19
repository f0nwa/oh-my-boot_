package control

import (
	"archive/zip"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"pxe-platform/internal/drivers"
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
	render(w, "drivers", pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message"), DriverPackages: packages})
}

func (s *Server) driversImport(w http.ResponseWriter, r *http.Request) {
	if s.DriverPackages == nil || r.MultipartForm == nil {
		http.Error(w, "Импорт драйверов недоступен", http.StatusServiceUnavailable)
		return
	}
	defer r.MultipartForm.RemoveAll()
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
	inventory, err := s.DriverPackages.ImportDriverPackage(r.Context(), archive, size)
	if err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "error", "agent")
		http.Error(w, "Не удалось проверить пакет драйвера: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.CreateDriverPackage(r.Context(), inventory); err != nil {
		_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "error", "database")
		http.Error(w, "Не удалось добавить пакет в каталог", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "driver.import", "ok", inventory.SHA256)
	http.Redirect(w, r, "/drivers?message=Пакет+драйвера+добавлен", http.StatusSeeOther)
}

func spoolDriverUpload(r *http.Request) (*os.File, int64, error) {
	archive, err := os.CreateTemp("", "pxe-driver-upload-*.zip")
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
