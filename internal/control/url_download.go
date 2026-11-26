package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"omb/internal/images"
)

const (
	urlDownloadMaxConcurrent = 3
	urlDownloadJobTTL        = 30 * time.Minute
	urlDownloadProbeTimeout  = 15 * time.Second
	urlDownloadMaxDuration   = 4 * time.Hour
)

type imageOptionsUploader interface {
	UploadImageWithOptions(context.Context, int64, string, string, string, io.Reader, int64, images.ReceiveOptions) (images.UploadResult, error)
}

type URLDownloadJob struct {
	ID               string     `json:"id"`
	URL              string     `json:"url"`
	Host             string     `json:"host"`
	Filename         string     `json:"filename"`
	StorageID        int64      `json:"storage_id"`
	StorageName      string     `json:"storage_name"`
	Folder           string     `json:"folder"`
	Path             string     `json:"path"`
	State            string     `json:"state"` // running | done | error | canceled
	BytesDone        int64      `json:"bytes_done"`
	BytesTotal       int64      `json:"bytes_total"`
	Percent          int        `json:"percent"`
	SpeedBPS         int64      `json:"speed_bps"`
	ChecksumAlgo     string     `json:"checksum_algo,omitempty"`
	ChecksumVerified bool       `json:"checksum_verified,omitempty"`
	Error            string     `json:"error,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
	Author           string     `json:"author,omitempty"`
}

type urlDownloadEntry struct {
	job    URLDownloadJob
	cancel context.CancelFunc
}

type URLDownloadManager struct {
	mu   sync.Mutex
	jobs map[string]*urlDownloadEntry
}

func NewURLDownloadManager() *URLDownloadManager {
	return &URLDownloadManager{jobs: map[string]*urlDownloadEntry{}}
}

func newDownloadJobID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

func (m *URLDownloadManager) cleanupLocked(now time.Time) {
	for id, e := range m.jobs {
		if e.job.State != "running" && e.job.FinishedAt != nil && now.Sub(*e.job.FinishedAt) > urlDownloadJobTTL {
			delete(m.jobs, id)
		}
	}
}

func (m *URLDownloadManager) reserve(job URLDownloadJob, cancel context.CancelFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(time.Now())
	running := 0
	for _, e := range m.jobs {
		if e.job.State == "running" {
			running++
			if e.job.StorageID == job.StorageID {
				return fmt.Errorf("В это хранилище уже идёт загрузка по URL")
			}
		}
	}
	if running >= urlDownloadMaxConcurrent {
		return fmt.Errorf("Достигнут лимит одновременных загрузок по URL (%d)", urlDownloadMaxConcurrent)
	}
	m.jobs[job.ID] = &urlDownloadEntry{job: job, cancel: cancel}
	return nil
}

func (m *URLDownloadManager) updateProgress(id string, done, total, speedBPS int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.jobs[id]
	if !ok || e.job.State != "running" {
		return
	}
	e.job.BytesDone = done
	if total > 0 {
		e.job.BytesTotal = total
		pct := int(done * 100 / total)
		if pct > 100 {
			pct = 100
		}
		if pct < 0 {
			pct = 0
		}
		e.job.Percent = pct
	} else {
		e.job.BytesTotal = -1
		e.job.Percent = -1
	}
	e.job.SpeedBPS = speedBPS
}

func (m *URLDownloadManager) finish(id, state, finalPath string, finalSize int64, checksumVerified bool, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.jobs[id]
	if !ok {
		return
	}
	now := time.Now()
	e.job.State = state
	e.job.FinishedAt = &now
	e.job.SpeedBPS = 0
	e.cancel = nil
	if finalPath != "" {
		e.job.Path = finalPath
	}
	if state == "done" {
		if finalSize > 0 {
			e.job.BytesDone = finalSize
			e.job.BytesTotal = finalSize
		}
		e.job.Percent = 100
		e.job.ChecksumVerified = checksumVerified
		e.job.Error = ""
	} else if errMsg != "" {
		e.job.Error = errMsg
	}
}

func (m *URLDownloadManager) List() []URLDownloadJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked(time.Now())
	list := make([]URLDownloadJob, 0, len(m.jobs))
	for _, e := range m.jobs {
		list = append(list, e.job)
	}
	sort.Slice(list, func(i, j int) bool {
		if (list[i].State == "running") != (list[j].State == "running") {
			return list[i].State == "running"
		}
		return list[i].StartedAt.After(list[j].StartedAt)
	})
	return list
}

func (m *URLDownloadManager) Cancel(id string) bool {
	m.mu.Lock()
	e, ok := m.jobs[id]
	if !ok || e.job.State != "running" || e.cancel == nil {
		m.mu.Unlock()
		return false
	}
	cancel := e.cancel
	m.mu.Unlock()
	cancel()
	return true
}

func (m *URLDownloadManager) Dismiss(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.jobs[id]
	if !ok || e.job.State == "running" {
		return false
	}
	delete(m.jobs, id)
	return true
}

type progressTrackingReader struct {
	src        io.Reader
	total      int64
	done       int64
	lastReport time.Time
	winStart   time.Time
	winBytes   int64
	speedBPS   int64
	onProgress func(done, total, speedBPS int64)
}

func newProgressTrackingReader(src io.Reader, total int64, onProgress func(done, total, speedBPS int64)) *progressTrackingReader {
	now := time.Now()
	return &progressTrackingReader{
		src:        src,
		total:      total,
		lastReport: now,
		winStart:   now,
		onProgress: onProgress,
	}
}

func (r *progressTrackingReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.done += int64(n)
		r.winBytes += int64(n)
		now := time.Now()
		elapsed := now.Sub(r.winStart)
		if elapsed >= 500*time.Millisecond {
			sec := elapsed.Seconds()
			if sec > 0 {
				r.speedBPS = int64(float64(r.winBytes) / sec)
			}
			if elapsed >= 2*time.Second {
				r.winStart = now
				r.winBytes = 0
			}
		}
		if now.Sub(r.lastReport) >= 350*time.Millisecond {
			r.lastReport = now
			if r.onProgress != nil {
				r.onProgress(r.done, r.total, r.speedBPS)
			}
		}
	}
	return n, err
}

func (s *Server) urlDownloadHTTPClient(insecureTLS bool, timeout time.Duration) *http.Client {
	if s.urlDownloadClient != nil {
		return s.urlDownloadClient(insecureTLS, timeout)
	}
	return NewURLDownloadHTTPClient(insecureTLS, timeout, nil)
}

func (s *Server) urlDownloadsManager() *URLDownloadManager {
	if s.urlDownloads == nil {
		s.urlDownloads = NewURLDownloadManager()
	}
	return s.urlDownloads
}

func writeURLJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

func friendlyURLNetworkError(lang string, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errInvalidDownloadURL) {
		return translate(lang, "Укажите корректный URL с протоколом http:// или https://")
	}
	if errors.Is(err, errBlockedDownloadIP) {
		return translate(lang, "Доступ к этому адресу запрещён политикой безопасности (loopback или link-local)")
	}
	if errors.Is(err, context.Canceled) {
		return translate(lang, "Загрузка отменена")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return translate(lang, "Превышено время ожидания ответа удалённого сервера")
	}
	msg := err.Error()
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "lookup ") {
		return translate(lang, "Не удалось разрешить доменное имя (нет DNS или закрытый контур)")
	}
	if strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:") {
		return translate(lang, "Ошибка проверки TLS-сертификата. Для внутреннего зеркала включите опцию пропуска проверки TLS.")
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "no route to host") || strings.Contains(msg, "network is unreachable") {
		return translate(lang, "Не удалось подключиться к удалённому серверу (адрес недоступен из сети OMB)")
	}
	return translate(lang, "Ошибка подключения к удалённому серверу")
}

func (s *Server) imagesURLProbe(w http.ResponseWriter, r *http.Request) {
	lang := normalizeLanguage(requestInfo(r).User.Language)
	var req struct {
		URL         string `json:"url"`
		StorageID   int64  `json:"storage_id"`
		InsecureTLS bool   `json:"insecure_tls"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := dec.Decode(&req); err != nil {
		writeURLJSONError(w, http.StatusBadRequest, translate(lang, "Неверный формат запроса"))
		return
	}
	if _, err := ValidateDownloadURL(req.URL); err != nil {
		writeURLJSONError(w, http.StatusBadRequest, friendlyURLNetworkError(lang, err))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), urlDownloadProbeTimeout)
	defer cancel()
	client := s.urlDownloadHTTPClient(req.InsecureTLS, urlDownloadProbeTimeout)
	probe, err := ProbeURL(ctx, client, req.URL)
	if err != nil {
		writeURLJSONError(w, http.StatusBadGateway, friendlyURLNetworkError(lang, err))
		return
	}
	if probe.StatusCode < 200 || probe.StatusCode >= 300 {
		writeURLJSONError(w, http.StatusBadGateway, fmt.Sprintf("%s (HTTP %d)", translate(lang, "Удалённый сервер вернул ошибку"), probe.StatusCode))
		return
	}
	if probe.Size > images.MaxISOBytes {
		writeURLJSONError(w, http.StatusBadRequest, translate(lang, "Размер файла превышает допустимый предел 32 ГБ"))
		return
	}
	var freeBytes int64
	fitsStorage := true
	if req.StorageID > 0 {
		if st, ok := s.uploadStorage(r.Context(), strconv.FormatInt(req.StorageID, 10)); ok {
			freeBytes = st.Free
			if probe.Size > 0 && st.Free > 0 && probe.Size > st.Free {
				fitsStorage = false
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"final_url":    probe.FinalURL,
		"host":         probe.Host,
		"filename":     probe.Filename,
		"size":         probe.Size,
		"content_type": probe.ContentType,
		"status_code":  probe.StatusCode,
		"free_bytes":   freeBytes,
		"fits_storage": fitsStorage,
	})
}

func (s *Server) imagesURLDownloadStart(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	lang := normalizeLanguage(info.User.Language)
	if s.Images == nil {
		writeURLJSONError(w, http.StatusServiceUnavailable, translate(lang, "Служба загрузки недоступна"))
		return
	}
	var req struct {
		URL           string `json:"url"`
		Filename      string `json:"filename"`
		StorageID     int64  `json:"storage_id"`
		Folder        string `json:"folder"`
		ChecksumAlgo  string `json:"checksum_algo"`
		ChecksumValue string `json:"checksum_value"`
		InsecureTLS   bool   `json:"insecure_tls"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if err := dec.Decode(&req); err != nil {
		writeURLJSONError(w, http.StatusBadRequest, translate(lang, "Неверный формат запроса"))
		return
	}
	parsedURL, err := ValidateDownloadURL(req.URL)
	if err != nil {
		writeURLJSONError(w, http.StatusBadRequest, friendlyURLNetworkError(lang, err))
		return
	}
	filename := strings.TrimSpace(req.Filename)
	if filename == "" {
		filename = filenameFromURL(parsedURL)
	}
	if !images.ValidImageName(filename, images.ImageKindISO) {
		writeURLJSONError(w, http.StatusBadRequest, translate(lang, "Неверное имя ISO"))
		return
	}
	checksumAlgo, checksumValue, err := images.ValidateChecksum(req.ChecksumAlgo, req.ChecksumValue)
	if err != nil {
		writeURLJSONError(w, http.StatusBadRequest, translate(lang, "Проверьте алгоритм и формат контрольной суммы (hex)"))
		return
	}
	storage, ok := s.uploadStorage(r.Context(), strconv.FormatInt(req.StorageID, 10))
	if !ok || (storage.Kind == "smb" && s.SMB == nil) || (storage.Kind == "nfs" && s.NFS == nil) {
		writeURLJSONError(w, http.StatusNotFound, translate(lang, "Хранилище недоступно"))
		return
	}
	if _, locked := s.loadMaintenanceLookup(r.Context()).StorageLock(storage.ID); locked {
		writeURLJSONError(w, http.StatusConflict, translate(lang, "Хранилище в режиме обслуживания"))
		return
	}
	if storage.Kind == "smb" {
		if err := s.SMB.EnableUpload(r.Context(), storage.Key, storage.Server, storage.Share, storage.Subpath); err != nil {
			_ = s.Store.Audit(r.Context(), info.User.ID, "iso.download_url", "mount-error", strconv.FormatInt(storage.ID, 10))
			writeURLJSONError(w, http.StatusServiceUnavailable, translate(lang, "Не удалось подключить хранилище для записи. Проверьте права SMB."))
			return
		}
	}
	if storage.Kind == "nfs" {
		if err := s.NFS.EnableNFSUpload(r.Context(), storage.Key, storage.Server, storage.ExportPath, storage.NFSVersion); err != nil {
			_ = s.Store.Audit(r.Context(), info.User.ID, "iso.download_url", "mount-error", strconv.FormatInt(storage.ID, 10))
			writeURLJSONError(w, http.StatusServiceUnavailable, translate(lang, "Не удалось подключить хранилище для записи. Проверьте права NFS."))
			return
		}
	}

	folder := strings.TrimSpace(req.Folder)
	relPath := filename
	if folder != "" {
		relPath = folder + "/" + filename
	}
	jobCtx, cancel := context.WithTimeout(context.Background(), urlDownloadMaxDuration)
	job := URLDownloadJob{
		ID:           newDownloadJobID(),
		URL:          redactDownloadURL(parsedURL),
		Host:         parsedURL.Hostname(),
		Filename:     filename,
		StorageID:    storage.ID,
		StorageName:  storage.Name,
		Folder:       folder,
		Path:         relPath,
		State:        "running",
		BytesTotal:   -1,
		Percent:      -1,
		ChecksumAlgo: checksumAlgo,
		StartedAt:    time.Now(),
		Author:       info.User.Username,
	}
	mgr := s.urlDownloadsManager()
	if err := mgr.reserve(job, cancel); err != nil {
		cancel()
		writeURLJSONError(w, http.StatusConflict, translate(lang, err.Error()))
		return
	}

	go s.runURLDownload(jobCtx, cancel, job, parsedURL.String(), images.ReceiveOptions{
		ChecksumAlgo:  checksumAlgo,
		ChecksumValue: checksumValue,
	}, req.InsecureTLS, info.User.ID, lang)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "job": job})
}

func (s *Server) runURLDownload(ctx context.Context, cancel context.CancelFunc, job URLDownloadJob, rawURL string, opts images.ReceiveOptions, insecureTLS bool, actorID int64, lang string) {
	defer cancel()
	mgr := s.urlDownloadsManager()
	client := s.urlDownloadHTTPClient(insecureTLS, 0)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		mgr.finish(job.ID, "error", "", 0, false, friendlyURLNetworkError(lang, err))
		_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "error", strconv.FormatInt(job.StorageID, 10))
		return
	}
	req.Header.Set("User-Agent", urlDownloadUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
			mgr.finish(job.ID, "canceled", "", 0, false, translate(lang, "Загрузка отменена"))
			_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "canceled", strconv.FormatInt(job.StorageID, 10))
			return
		}
		mgr.finish(job.ID, "error", "", 0, false, friendlyURLNetworkError(lang, err))
		_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "error", strconv.FormatInt(job.StorageID, 10))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		mgr.finish(job.ID, "error", "", 0, false, fmt.Sprintf("%s (HTTP %d)", translate(lang, "Удалённый сервер вернул ошибку"), resp.StatusCode))
		_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "error", strconv.FormatInt(job.StorageID, 10))
		return
	}
	if resp.ContentLength > images.MaxISOBytes {
		mgr.finish(job.ID, "error", "", 0, false, translate(lang, "Размер файла превышает допустимый предел 32 ГБ"))
		_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "error", strconv.FormatInt(job.StorageID, 10))
		return
	}

	size := resp.ContentLength
	if size <= 0 {
		size = -1
	}
	mgr.updateProgress(job.ID, 0, size, 0)
	tracked := newProgressTrackingReader(resp.Body, size, func(done, total, speedBPS int64) {
		mgr.updateProgress(job.ID, done, total, speedBPS)
	})

	var result images.UploadResult
	if uploader, ok := s.Images.(imageOptionsUploader); ok {
		result, err = uploader.UploadImageWithOptions(ctx, job.StorageID, job.Folder, job.Filename, images.ImageKindISO, tracked, size, opts)
	} else if uploader, ok := s.Images.(imageUploader); ok && size > 0 && opts.ChecksumAlgo == "" {
		result, err = uploader.UploadImage(ctx, job.StorageID, job.Folder, job.Filename, images.ImageKindISO, tracked, size)
	} else if size > 0 && opts.ChecksumAlgo == "" {
		result, err = s.Images.UploadISO(ctx, job.StorageID, job.Folder, job.Filename, tracked, size)
	} else {
		err = fmt.Errorf("image upload: 503 streaming upload unsupported")
	}

	if err != nil {
		if ctx.Err() == context.Canceled || errors.Is(err, context.Canceled) {
			mgr.finish(job.ID, "canceled", "", 0, false, translate(lang, "Загрузка отменена"))
			_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "canceled", strconv.FormatInt(job.StorageID, 10))
			return
		}
		mgr.finish(job.ID, "error", "", 0, false, translate(lang, urlDownloadUploadErrorMessage(err)))
		_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "error", strconv.FormatInt(job.StorageID, 10))
		return
	}

	_ = s.Store.Audit(context.Background(), actorID, "iso.download_url", "ok", strconv.FormatInt(job.StorageID, 10)+" "+result.Path+" "+result.SHA256)
	s.InvalidateResourceAuthorsCache()
	mgr.finish(job.ID, "done", result.Path, result.Size, opts.ChecksumAlgo != "", "")
}

func urlDownloadUploadErrorMessage(err error) string {
	text := err.Error()
	switch {
	case containsUploadStatus(text, "422"):
		return "Контрольная сумма не совпала — временный файл удалён"
	case containsUploadStatus(text, "409"):
		return "Файл с таким именем уже существует или хранилище занято"
	case containsUploadStatus(text, "507"):
		return "Недостаточно свободного места в хранилище"
	case containsUploadStatus(text, "400"):
		return "Проверьте имя файла, размер или выбранную папку"
	default:
		return "Не удалось сохранить образ в хранилище"
	}
}

func (s *Server) imagesURLDownloadsList(w http.ResponseWriter, r *http.Request) {
	jobs := s.urlDownloadsManager().List()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"jobs": jobs})
}

func (s *Server) imagesURLDownloadCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.urlDownloadsManager().Cancel(id) {
		writeURLJSONError(w, http.StatusNotFound, translate(normalizeLanguage(requestInfo(r).User.Language), "Активная загрузка не найдена"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (s *Server) imagesURLDownloadDismiss(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.urlDownloadsManager().Dismiss(id) {
		writeURLJSONError(w, http.StatusNotFound, translate(normalizeLanguage(requestInfo(r).User.Language), "Завершённая загрузка не найдена"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
