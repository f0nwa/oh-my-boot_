package control

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"omb/internal/autoinstall"
)

const maxUbuntuAutoinstallUploadBytes = autoinstall.MaxUploadBytes

func (s *Server) ubuntuAutoinstallUpload(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	if !safeMenuText(title, 80) {
		http.Error(w, "Укажите название скрипта (до 80 символов)", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Выберите файл user-data / YAML", http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxUbuntuAutoinstallUploadBytes+1))
	if err != nil {
		http.Error(w, "Не удалось прочитать загруженный файл", http.StatusBadRequest)
		return
	}
	clean, meta, err := autoinstall.ValidateUploadedUserData(raw)
	if err != nil {
		switch {
		case errors.Is(err, autoinstall.ErrUploadTooLarge):
			http.Error(w, "Файл больше 64 КиБ", http.StatusRequestEntityTooLarge)
		case errors.Is(err, autoinstall.ErrUploadEmpty):
			http.Error(w, "Файл пустой", http.StatusBadRequest)
		case errors.Is(err, autoinstall.ErrUploadEncoding):
			http.Error(w, "Файл должен быть в кодировке UTF-8", http.StatusBadRequest)
		case errors.Is(err, autoinstall.ErrUploadCloudConfig):
			http.Error(w, "Файл должен начинаться с #cloud-config", http.StatusBadRequest)
		case errors.Is(err, autoinstall.ErrUploadMissingSection):
			http.Error(w, "В YAML отсутствует секция autoinstall", http.StatusBadRequest)
		case errors.Is(err, autoinstall.ErrUploadVersion):
			http.Error(w, "В секции autoinstall требуется version: 1", http.StatusBadRequest)
		default:
			http.Error(w, "Некорректный YAML-файл Ubuntu Autoinstall", http.StatusBadRequest)
		}
		return
	}

	sum := sha256.Sum256(clean)
	digest := hex.EncodeToString(sum[:])
	saved, err := s.Store.CreateUbuntuAutoinstall(r.Context(), UbuntuAutoinstallFile{
		Title:      title,
		SourceKind: "uploaded",
		UserData:   clean,
		SHA256:     digest,
		State:      "validated",
		DiskMode:   meta.DiskMode,
		DiskSerial: meta.DiskSerial,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "Такой скрипт Ubuntu Autoinstall уже загружен", http.StatusConflict)
			return
		}
		http.Error(w, "Не удалось сохранить файл Autoinstall", http.StatusServiceUnavailable)
		return
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "ubuntu-autoinstall.upload", "ok", strconv.FormatInt(saved.ID, 10))
	s.InvalidateResourceAuthorsCache()
	msg := fmt.Sprintf("Скрипт «%s» загружен", saved.Title)
	http.Redirect(w, r, "/answers?message="+url.QueryEscape(msg)+"#ubuntu-autoinstall", http.StatusSeeOther)
}
