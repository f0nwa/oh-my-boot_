package images

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrMigrationNotFound   = errors.New("migration not found")
	ErrMigrationInProgress = errors.New("migration already in progress for this image")
	ErrSameStorage         = errors.New("source and target storage cannot be the same")
	ErrSourceNotFound      = errors.New("source image file not found")
	ErrTargetExists        = errors.New("target image file already exists")
)

type MigrationJob struct {
	ID               string    `json:"id"`
	SourceStorageID  int64     `json:"source_storage_id"`
	Path             string    `json:"path"`
	TargetStorageID  int64     `json:"target_storage_id"`
	State            string    `json:"state"` // "copying", "ready_to_rebind", "rebinding", "completed", "failed", "canceled"
	TotalBytes       int64     `json:"total_bytes"`
	CopiedBytes      int64     `json:"copied_bytes"`
	Percent          int       `json:"percent"`
	SpeedBytesPerSec int64     `json:"speed_bytes_per_sec"`
	Error            string    `json:"error,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	cancel       context.CancelFunc
	tempFilePath string
	dstFinalPath string
	srcFullPath   string
	srcDeleteRoot string
}

type MigrationEngine struct {
	repo   APIRepository
	mu     sync.Mutex
	jobs   map[string]*MigrationJob
	active map[string]string // key: "storage_id:path" -> job.ID
}

func NewMigrationEngine(repo APIRepository) *MigrationEngine {
	return &MigrationEngine{
		repo:   repo,
		jobs:   make(map[string]*MigrationJob),
		active: make(map[string]string),
	}
}

func (e *MigrationEngine) StartMigration(ctx context.Context, sourceStorageID int64, relPath string, srcDeleteRoot string, targetStorageID int64, targetWriteRoot string) (*MigrationJob, error) {
	relPath = filepath.Clean(relPath)
	if sourceStorageID <= 0 || targetStorageID <= 0 || relPath == "" || strings.HasPrefix(relPath, "..") {
		return nil, errors.New("invalid migration parameters")
	}
	if sourceStorageID == targetStorageID {
		return nil, ErrSameStorage
	}

	srcStorage, err := e.repo.Storage(ctx, sourceStorageID)
	if err != nil {
		return nil, fmt.Errorf("source storage error: %w", err)
	}
	targetStorage, err := e.repo.Storage(ctx, targetStorageID)
	if err != nil {
		return nil, fmt.Errorf("target storage error: %w", err)
	}

	srcFullPath := filepath.Join(srcStorage.Root, relPath)
	srcInfo, err := os.Stat(srcFullPath)
	if err != nil {
		return nil, ErrSourceNotFound
	}
	if !srcInfo.Mode().IsRegular() {
		return nil, errors.New("source is not a regular file")
	}
	totalBytes := srcInfo.Size()

	dstFinalPath := filepath.Join(targetWriteRoot, relPath)
	if _, err := os.Stat(dstFinalPath); err == nil {
		return nil, ErrTargetExists
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Prepare destination directory
	dstDir := filepath.Dir(dstFinalPath)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create target directory: %w", err)
	}

	tempFile, err := os.CreateTemp(dstDir, ".omb-migrate-*.part")
	if err != nil {
		return nil, fmt.Errorf("cannot create target temp file: %w", err)
	}
	tempPath := tempFile.Name()

	e.mu.Lock()
	srcKey := fmt.Sprintf("%d:%s", sourceStorageID, relPath)
	dstKey := fmt.Sprintf("%d:%s", targetStorageID, relPath)
	if _, exists := e.active[srcKey]; exists {
		e.mu.Unlock()
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return nil, ErrMigrationInProgress
	}
	if _, exists := e.active[dstKey]; exists {
		e.mu.Unlock()
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return nil, ErrMigrationInProgress
	}

	jobID := fmt.Sprintf("mig_%d_%d_%d", time.Now().UnixNano(), sourceStorageID, targetStorageID)
	jobCtx, cancel := context.WithCancel(context.Background())

	job := &MigrationJob{
		ID:               jobID,
		SourceStorageID:  sourceStorageID,
		Path:             relPath,
		TargetStorageID:  targetStorageID,
		State:            "copying",
		TotalBytes:       totalBytes,
		CopiedBytes:      0,
		Percent:          0,
		StartedAt:        time.Now(),
		UpdatedAt:        time.Now(),
		cancel:           cancel,
		tempFilePath:     tempPath,
		dstFinalPath:     dstFinalPath,
		srcFullPath:      srcFullPath,
		srcDeleteRoot:    srcDeleteRoot,
	}

	e.jobs[jobID] = job
	e.active[srcKey] = jobID
	e.active[dstKey] = jobID
	e.mu.Unlock()

	go e.runCopy(jobCtx, job, tempFile, targetStorage)

	return job, nil
}

func (e *MigrationEngine) runCopy(ctx context.Context, job *MigrationJob, tempFile *os.File, targetStorage Storage) {
	defer tempFile.Close()

	srcFile, err := os.Open(job.srcFullPath)
	if err != nil {
		e.failJob(job, fmt.Sprintf("cannot open source file: %v", err))
		return
	}
	defer srcFile.Close()

	buf := make([]byte, 1024*1024) // 1MB buffer
	var lastTime = time.Now()
	var lastBytes int64 = 0

	for {
		select {
		case <-ctx.Done():
			e.cancelJob(job)
			return
		default:
		}

		nr, readErr := srcFile.Read(buf)
		if nr > 0 {
			nw, writeErr := tempFile.Write(buf[:nr])
			if writeErr != nil {
				e.failJob(job, fmt.Sprintf("write error: %v", writeErr))
				return
			}
			if nr != nw {
				e.failJob(job, "short write")
				return
			}

			e.mu.Lock()
			job.CopiedBytes += int64(nw)
			if job.TotalBytes > 0 {
				job.Percent = int((job.CopiedBytes * 100) / job.TotalBytes)
			}
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			if elapsed >= 0.5 {
				bytesDelta := job.CopiedBytes - lastBytes
				job.SpeedBytesPerSec = int64(float64(bytesDelta) / elapsed)
				job.UpdatedAt = now
				lastTime = now
				lastBytes = job.CopiedBytes
			}
			e.mu.Unlock()
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			e.failJob(job, fmt.Sprintf("read error: %v", readErr))
			return
		}
	}

	// Flush and close temp file before renaming
	if err := tempFile.Sync(); err != nil {
		e.failJob(job, fmt.Sprintf("sync error: %v", err))
		return
	}
	_ = tempFile.Close()

	// Atomic rename to final path
	if err := promoteNoReplace(job.tempFilePath, job.dstFinalPath); err != nil {
		e.failJob(job, fmt.Sprintf("promote error: %v", err))
		return
	}

	// Record upload in catalog
	img := Image{
		StorageID: job.TargetStorageID,
		Path:      job.Path,
		Size:      job.TotalBytes,
		Modified:  time.Now(),
		Present:   true,
	}
	if err := e.repo.RecordUpload(context.Background(), img); err != nil {
		e.failJob(job, fmt.Sprintf("catalog record error: %v", err))
		return
	}

	e.mu.Lock()
	job.State = "ready_to_rebind"
	job.Percent = 100
	job.CopiedBytes = job.TotalBytes
	job.UpdatedAt = time.Now()
	e.mu.Unlock()
}

func (e *MigrationEngine) failJob(job *MigrationJob, errMsg string) {
	_ = os.Remove(job.tempFilePath)
	e.mu.Lock()
	defer e.mu.Unlock()
	job.State = "failed"
	job.Error = errMsg
	job.UpdatedAt = time.Now()
	delete(e.active, fmt.Sprintf("%d:%s", job.SourceStorageID, job.Path))
	delete(e.active, fmt.Sprintf("%d:%s", job.TargetStorageID, job.Path))
}

func (e *MigrationEngine) cancelJob(job *MigrationJob) {
	_ = os.Remove(job.tempFilePath)
	e.mu.Lock()
	defer e.mu.Unlock()
	job.State = "canceled"
	job.UpdatedAt = time.Now()
	delete(e.active, fmt.Sprintf("%d:%s", job.SourceStorageID, job.Path))
	delete(e.active, fmt.Sprintf("%d:%s", job.TargetStorageID, job.Path))
}

func (e *MigrationEngine) GetMigration(id string) (*MigrationJob, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	job, ok := e.jobs[id]
	if !ok {
		return nil, false
	}
	clone := *job
	return &clone, true
}

func (e *MigrationEngine) ListMigrations() []*MigrationJob {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := make([]*MigrationJob, 0, len(e.jobs))
	for _, j := range e.jobs {
		clone := *j
		list = append(list, &clone)
	}
	return list
}

func (e *MigrationEngine) CancelMigration(id string) error {
	e.mu.Lock()
	job, ok := e.jobs[id]
	if !ok {
		e.mu.Unlock()
		return ErrMigrationNotFound
	}
	if job.State != "copying" {
		e.mu.Unlock()
		return fmt.Errorf("cannot cancel migration in state %s", job.State)
	}
	cancel := job.cancel
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

func (e *MigrationEngine) CommitDeleteSource(ctx context.Context, id string) error {
	e.mu.Lock()
	job, ok := e.jobs[id]
	if !ok {
		e.mu.Unlock()
		return ErrMigrationNotFound
	}
	if job.State != "ready_to_rebind" && job.State != "rebinding" {
		e.mu.Unlock()
		return fmt.Errorf("cannot commit migration in state %s", job.State)
	}
	e.mu.Unlock()

	// 1. Delete record from catalog
	if err := e.repo.DeleteImage(ctx, job.SourceStorageID, job.Path); err != nil {
		return fmt.Errorf("failed to delete source image from catalog: %w", err)
	}

	// 2. Delete source file from filesystem
	deletePath := job.srcFullPath
	if job.srcDeleteRoot != "" {
		deletePath = filepath.Join(job.srcDeleteRoot, job.Path)
	}
	if err := os.Remove(deletePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove source file from disk: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	job.State = "completed"
	job.UpdatedAt = time.Now()
	delete(e.active, fmt.Sprintf("%d:%s", job.SourceStorageID, job.Path))
	delete(e.active, fmt.Sprintf("%d:%s", job.TargetStorageID, job.Path))
	return nil
}
