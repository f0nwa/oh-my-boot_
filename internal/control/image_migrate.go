package control

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"omb/internal/agent"
	"omb/internal/images"
)

var (
	ErrImageInUse = errors.New("образ используется активной сетевой установкой")
)

type smbSessionsProvider interface {
	SMBSessions(context.Context) (agent.SMBSessions, error)
}

func isNilProvider(provider any) bool {
	if provider == nil {
		return true
	}
	v := reflect.ValueOf(provider)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func getActiveSMBSessions(ctx context.Context, provider any) ([]agent.SMBSession, error) {
	if isNilProvider(provider) {
		return nil, nil
	}
	if p, ok := provider.(interface {
		SMBSessions(context.Context) (agent.SMBSessions, error)
	}); ok && p != nil {
		s, err := p.SMBSessions(ctx)
		return s.Sessions, err
	}
	if p, ok := provider.(interface {
		SMBSessions(context.Context) ([]agent.SMBSession, error)
	}); ok && p != nil {
		return p.SMBSessions(ctx)
	}
	return nil, nil
}

type MigrationCoordinator struct {
	store      Store
	images     ImagesAPI
	windowsISO WindowsISOAPI
	ubuntuISO  UbuntuISOAPI
	agent      any
	nfs        any
	mu         *sync.Mutex
	onRebind   func()
}

func NewMigrationCoordinator(store Store, img ImagesAPI, winISO WindowsISOAPI, ubuISO UbuntuISOAPI, agent any) *MigrationCoordinator {
	return &MigrationCoordinator{
		store:      store,
		images:     img,
		windowsISO: winISO,
		ubuntuISO:  ubuISO,
		agent:      agent,
		mu:         &sync.Mutex{},
	}
}

func (c *MigrationCoordinator) CheckPreflight(ctx context.Context, sourceStorageID int64, path string, targetStorageID int64) error {
	if sourceStorageID <= 0 || targetStorageID <= 0 || strings.TrimSpace(path) == "" {
		return errors.New("некорректные параметры миграции")
	}
	if sourceStorageID == targetStorageID {
		return errors.New("исходное и целевое хранилище не могут совпадать")
	}

	catalog, err := c.images.Catalog(ctx)
	if err != nil {
		return fmt.Errorf("каталог образов недоступен: %w", err)
	}

	var sourceStorage, targetStorage *images.Storage
	for i := range catalog.Storages {
		if catalog.Storages[i].ID == sourceStorageID {
			sourceStorage = &catalog.Storages[i]
		}
		if catalog.Storages[i].ID == targetStorageID {
			targetStorage = &catalog.Storages[i]
		}
	}

	if sourceStorage == nil {
		return errors.New("исходное хранилище не найдено")
	}
	if targetStorage == nil {
		return errors.New("целевое хранилище не найдено")
	}
	if sourceStorage.State != "ready" {
		return fmt.Errorf("исходное хранилище недоступно (статус: %s)", sourceStorage.State)
	}
	if targetStorage.State != "ready" {
		return fmt.Errorf("целевое хранилище недоступно (статус: %s)", targetStorage.State)
	}
	if !sourceStorage.Uploads {
		return errors.New("исходное хранилище доступно только для чтения (удаление оригинала невозможно)")
	}
	if !targetStorage.Uploads {
		return errors.New("целевое хранилище доступно только для чтения")
	}
	if err := c.verifyNFSWritable(ctx, *sourceStorage, true); err != nil {
		return err
	}
	if err := c.verifyNFSWritable(ctx, *targetStorage, false); err != nil {
		return err
	}

	var sourceImage *images.Image
	for i := range catalog.Images {
		img := &catalog.Images[i]
		if img.StorageID == sourceStorageID && img.Path == path {
			sourceImage = img
			break
		}
	}
	if sourceImage == nil || !sourceImage.Present {
		return errors.New("исходный образ не найден")
	}

	for i := range catalog.Images {
		img := &catalog.Images[i]
		if img.StorageID == targetStorageID && img.Path == path && img.Present {
			return errors.New("файл с таким именем уже существует в целевом хранилище")
		}
	}

	requiredBytes := sourceImage.Size + (512 << 20)
	if targetStorage.Free < requiredBytes {
		return fmt.Errorf("на целевом хранилище недостаточно свободного места (требуется %s, доступно %s)",
			humanSize("ru", requiredBytes), humanSize("ru", targetStorage.Free))
	}

	profiles, err := c.store.WindowsProfiles(ctx)
	if err == nil && len(profiles) > 0 {
		var matched []WindowsProfile
		for _, p := range profiles {
			if p.StorageID == sourceStorageID && p.Path == path {
				matched = append(matched, p)
			}
		}

		if len(matched) > 0 {
			sessions, _ := getActiveSMBSessions(ctx, c.agent)
			for _, s := range sessions {
				if s.Ended != nil {
					continue
				}
				sessionProfile := strings.TrimSpace(s.Profile)
				if sessionProfile == "" {
					continue
				}
				for _, p := range matched {
					if sessionProfile == strconv.FormatInt(p.ID, 10) ||
						strings.EqualFold(sessionProfile, strings.TrimSpace(p.Title)) ||
						(p.Folder != "" && strings.EqualFold(sessionProfile, strings.TrimSpace(p.Folder))) {
						return fmt.Errorf("%w: клиент %s выполняет установку через \\\\ombisos\\%s", ErrImageInUse, s.IP, sessionProfile)
					}
				}
			}
		}
	}

	return nil
}

func (c *MigrationCoordinator) verifyNFSWritable(ctx context.Context, st images.Storage, isSource bool) error {
	if st.Kind != "nfs" || st.Key == "" || isNilProvider(c.nfs) {
		return nil
	}
	checker, ok := c.nfs.(interface {
		CheckNFSUpload(context.Context, string) (bool, error)
	})
	if !ok {
		return nil
	}
	writable, err := checker.CheckNFSUpload(ctx, st.Key)
	if err != nil || writable {
		return nil
	}
	_ = c.images.SetNFSUploads(ctx, st.ID, false)
	if isSource {
		return errors.New("исходное хранилище доступно только для чтения (удаление оригинала невозможно)")
	}
	return errors.New("целевое хранилище доступно только для чтения")
}

func (c *MigrationCoordinator) StartMigration(ctx context.Context, user User, sourceStorageID int64, path string, targetStorageID int64) (*images.MigrationJob, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.CheckPreflight(ctx, sourceStorageID, path, targetStorageID); err != nil {
		return nil, err
	}

	job, err := c.images.StartMigration(ctx, sourceStorageID, path, targetStorageID)
	if err != nil {
		if errors.Is(err, images.ErrSourceNotWritable) || strings.Contains(err.Error(), "source storage is not writable") {
			return nil, errors.New("исходное хранилище доступно только для чтения (удаление оригинала невозможно)")
		}
		return nil, err
	}

	_, _ = c.store.Pool.Exec(ctx, `
		INSERT INTO image_migrations(id, source_storage_id, path, target_storage_id, state, total_bytes, copied_bytes, user_id, created_at, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW())
		ON CONFLICT(id) DO UPDATE SET state=EXCLUDED.state, total_bytes=EXCLUDED.total_bytes, updated_at=NOW()`,
		job.ID, sourceStorageID, path, targetStorageID, job.State, job.TotalBytes, job.CopiedBytes, user.ID)

	_ = c.store.Audit(ctx, user.ID, "iso.migrate_start", "ok", fmt.Sprintf("%d %s -> %d", sourceStorageID, path, targetStorageID))
	return job, nil
}

func (c *MigrationCoordinator) ProcessCompletedMigration(ctx context.Context, jobID string) error {
	if c.mu != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
	}

	job, err := c.images.GetMigration(ctx, jobID)
	if err != nil {
		return err
	}

	if job.State != "ready_to_rebind" && job.State != "completed" {
		return nil
	}

	var currentState string
	err = c.store.Pool.QueryRow(ctx, `SELECT state FROM image_migrations WHERE id=$1`, jobID).Scan(&currentState)
	if err == nil && currentState == "completed" {
		if job.State == "ready_to_rebind" {
			return c.finishDeleteSource(ctx, job)
		}
		return nil
	}

	_, _ = c.store.Pool.Exec(ctx, `UPDATE image_migrations SET state='rebinding', updated_at=NOW() WHERE id=$1`, jobID)

	catalog, err := c.images.Catalog(ctx)
	if err != nil {
		return fmt.Errorf("каталог недоступен при перепривязке: %w", err)
	}
	var targetRoot string
	for _, s := range catalog.Storages {
		if s.ID == job.TargetStorageID {
			targetRoot = s.Root
			break
		}
	}
	if targetRoot == "" {
		return fmt.Errorf("целевой корень хранилища %d не найден", job.TargetStorageID)
	}

	// 1. Rebind in agent: Windows
	profiles, _ := c.store.WindowsProfiles(ctx)
	if c.windowsISO != nil {
		for _, p := range profiles {
			if p.StorageID == job.SourceStorageID && p.Path == job.Path {
				req := agent.WindowsISORequest{
					ID:           p.ID,
					StorageRoot:  targetRoot,
					RelativePath: job.Path,
					BootMode:     p.BootMode,
				}
				if _, err := c.windowsISO.RebindWindowsISO(ctx, req); err != nil {
					_, _ = c.store.Pool.Exec(ctx, `UPDATE image_migrations SET state='failed', error=$2, updated_at=NOW() WHERE id=$1`, jobID, err.Error())
					return fmt.Errorf("ошибка перепривязки Windows ISO в агенте: %w", err)
				}
			}
		}
	}

	// 2. Rebind in agent: Ubuntu
	if c.ubuntuISO != nil {
		rows, err := c.store.Pool.Query(ctx, `SELECT boot_profile_id, sha256 FROM ubuntu_sources WHERE storage_id=$1 AND path=$2`, job.SourceStorageID, job.Path)
		if err == nil {
			type ubSource struct {
				bootProfileID int64
				digest        string
			}
			var ubs []ubSource
			for rows.Next() {
				var u ubSource
				if err := rows.Scan(&u.bootProfileID, &u.digest); err == nil {
					ubs = append(ubs, u)
				}
			}
			rows.Close()

			for _, u := range ubs {
				req := agent.UbuntuISORequest{
					WindowsISORequest: agent.WindowsISORequest{
						ID:           u.bootProfileID,
						StorageRoot:  targetRoot,
						RelativePath: job.Path,
					},
					SHA256: u.digest,
				}
				if err := c.ubuntuISO.RebindUbuntuISO(ctx, req); err != nil {
					_, _ = c.store.Pool.Exec(ctx, `UPDATE image_migrations SET state='failed', error=$2, updated_at=NOW() WHERE id=$1`, jobID, err.Error())
					return fmt.Errorf("ошибка перепривязки Ubuntu ISO в агенте: %w", err)
				}
			}
		}
	}

	// 3. PostgreSQL Transactional Rebind
	tx, err := c.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE windows_profiles
		SET storage_id=$1, storage_root=$2
		WHERE storage_id=$3 AND path=$4`,
		job.TargetStorageID, targetRoot, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("не удалось обновить windows_profiles: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE ubuntu_sources
		SET storage_id=$1
		WHERE storage_id=$2 AND path=$3`,
		job.TargetStorageID, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("не удалось обновить ubuntu_sources: %w", err)
	}

	// Move image display name
	if _, err := tx.Exec(ctx, `
		INSERT INTO image_names(storage_id, path, name)
		SELECT $1, path, name FROM image_names WHERE storage_id=$2 AND path=$3
		ON CONFLICT(storage_id, path) DO UPDATE SET name=EXCLUDED.name`,
		job.TargetStorageID, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("не удалось обновить image_names: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM image_names WHERE storage_id=$1 AND path=$2`, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("не удалось удалить старый image_names: %w", err)
	}

	// Move image inspection
	if _, err := tx.Exec(ctx, `
		INSERT INTO image_inspections(storage_id, path, storage_root, size_bytes, modified_at, kind)
		SELECT $1, path, $2, size_bytes, modified_at, kind FROM image_inspections WHERE storage_id=$3 AND path=$4
		ON CONFLICT(storage_id, path) DO UPDATE SET storage_root=EXCLUDED.storage_root, size_bytes=EXCLUDED.size_bytes, modified_at=EXCLUDED.modified_at, kind=EXCLUDED.kind`,
		job.TargetStorageID, targetRoot, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("не удалось обновить image_inspections: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM image_inspections WHERE storage_id=$1 AND path=$2`, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("не удалось удалить старый image_inspections: %w", err)
	}

	// Record audit event iso.migrate to preserve original author and upload date
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id, action, result, detail, occurred_at)
		VALUES((SELECT user_id FROM image_migrations WHERE id=$1), 'iso.migrate', 'ok', $2, NOW())`,
		jobID, fmt.Sprintf("%d %d %s", job.SourceStorageID, job.TargetStorageID, job.Path)); err != nil {
		return fmt.Errorf("не удалось записать audit_events: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE image_migrations SET state='completed', updated_at=NOW() WHERE id=$1`, jobID); err != nil {
		return fmt.Errorf("не удалось обновить статус image_migrations: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("не удалось зафиксировать транзакцию перепривязки: %w", err)
	}
	if c.onRebind != nil {
		c.onRebind()
	}

	// 4. Commit delete source on images engine
	return c.finishDeleteSource(ctx, job)
}

func (c *MigrationCoordinator) finishDeleteSource(ctx context.Context, job *images.MigrationJob) error {
	if err := c.images.CommitDeleteSource(ctx, job.ID); err != nil {
		if updated, getErr := c.images.GetMigration(ctx, job.ID); getErr == nil && updated.State == "failed" {
			errText := updated.Error
			if errText == "" {
				errText = err.Error()
			}
			if c.store.Pool != nil {
				_, _ = c.store.Pool.Exec(ctx, `UPDATE image_migrations SET state='failed', error=$2, updated_at=NOW() WHERE id=$1`, job.ID, errText)
			}
		}
		return fmt.Errorf("не удалось удалить исходный образ после перепривязки: %w", err)
	}
	if c.store.Pool != nil {
		_ = c.store.AuditSystem(ctx, "iso.migrate_success", "ok", fmt.Sprintf("%s (%d %s -> %d)", job.ID, job.SourceStorageID, job.Path, job.TargetStorageID))
	}
	if c.onRebind != nil {
		c.onRebind()
	}
	return nil
}

func (c *MigrationCoordinator) CancelMigration(ctx context.Context, user User, jobID string) error {
	if c.mu != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
	}

	if err := c.images.CancelMigration(ctx, jobID); err != nil {
		return err
	}
	_, _ = c.store.Pool.Exec(ctx, `UPDATE image_migrations SET state='canceled', updated_at=NOW() WHERE id=$1`, jobID)
	_ = c.store.Audit(ctx, user.ID, "iso.migrate_cancel", "ok", jobID)
	return nil
}

func (c *MigrationCoordinator) ActiveMigrations(ctx context.Context) ([]*images.MigrationJob, error) {
	list, err := c.images.ListMigrations(ctx)
	if err != nil {
		return nil, err
	}

	for _, job := range list {
		if job.State == "ready_to_rebind" {
			if err := c.ProcessCompletedMigration(ctx, job.ID); err != nil {
				log.Printf("image migration %s completion failed: %v", job.ID, err)
			}
		}
	}

	// Re-fetch list to get up to date states
	return c.images.ListMigrations(ctx)
}

func (s *Server) migrationCoordinator() *MigrationCoordinator {
	if s.Images == nil {
		return nil
	}
	var store Store
	if st, ok := s.Store.(Store); ok {
		store = st
	}
	var agentAPI any
	if !isNilProvider(s.Agent) {
		agentAPI = s.Agent
	}
	var nfsAPI any
	if !isNilProvider(s.NFS) {
		nfsAPI = s.NFS
	}
	return &MigrationCoordinator{
		store:      store,
		images:     s.Images,
		windowsISO: s.WindowsISO,
		ubuntuISO:  s.UbuntuISO,
		agent:      agentAPI,
		nfs:        nfsAPI,
		mu:         &s.migrationMu,
		onRebind:   s.InvalidateResourceAuthorsCache,
	}
}

func (s *Server) imageMigrate(w http.ResponseWriter, r *http.Request) {
	coord := s.migrationCoordinator()
	if coord == nil {
		http.Error(w, "Сервис миграции недоступен", http.StatusServiceUnavailable)
		return
	}

	srcStorageID, _ := strconv.ParseInt(r.FormValue("source_storage_id"), 10, 64)
	dstStorageID, _ := strconv.ParseInt(r.FormValue("target_storage_id"), 10, 64)
	path := strings.TrimSpace(r.FormValue("path"))

	info := requestInfo(r)
	job, err := coord.StartMigration(r.Context(), info.User, srcStorageID, path, dstStorageID)
	if err != nil {
		if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(job)
		return
	}

	http.Redirect(w, r, "/images", http.StatusSeeOther)
}

func (s *Server) imageMigrationsAPI(w http.ResponseWriter, r *http.Request) {
	coord := s.migrationCoordinator()
	if coord == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
		return
	}

	migrations, err := coord.ActiveMigrations(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(migrations)
}

func (s *Server) imageMigrateCancel(w http.ResponseWriter, r *http.Request) {
	coord := s.migrationCoordinator()
	if coord == nil {
		http.Error(w, "Сервис миграции недоступен", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	info := requestInfo(r)
	if err := coord.CancelMigration(r.Context(), info.User, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

//go:embed image_migrate.js
var imageMigrateJS []byte

func imageMigrateScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(imageMigrateJS)
}
