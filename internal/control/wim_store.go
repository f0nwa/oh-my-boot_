package control

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"omb/internal/agent"

	"github.com/jackc/pgx/v5"
)

const (
	WIMSourceKindISO  = "iso"
	WIMSourceKindFile = "file"
)

// WindowsBootImage represents a managed boot.wim entry in omb_control, either
// backed by a prepared Windows ISO (source_kind='iso') or by a standalone .wim
// file in a storage (source_kind='file').
type WindowsBootImage struct {
	ID               int64
	SourceKind       string
	WindowsProfileID int64
	StorageID        int64
	Path             string
	StorageRoot      string
	Size             int64
	Modified         *time.Time
	Title            string
	SHA256           string
	HasSetupExe      bool
	WIMInfo          agent.WindowsWIMInfo
	State            string
	Error            string
	CreatedAt        time.Time
}

// WindowsBootImageSource describes a standalone boot.wim file selected from
// the image catalog.
type WindowsBootImageSource struct {
	StorageID   int64
	Path        string
	StorageRoot string
	Size        int64
	Modified    time.Time
	Title       string
}

// WindowsInstallImage represents a managed standalone install.wim or
// install.esd file published read-only for Windows Setup.
type WindowsInstallImage struct {
	ID          int64
	StorageID   int64
	Path        string
	StorageRoot string
	InstallFile string
	Size        int64
	Modified    time.Time
	Title       string
	WIMInfo     agent.WindowsWIMInfo
	State       string
	Error       string
	CreatedAt   time.Time
}

// WindowsInstallImageSource describes a standalone install.wim or install.esd
// file selected from the image catalog.
type WindowsInstallImageSource struct {
	StorageID   int64
	Path        string
	StorageRoot string
	InstallFile string
	Size        int64
	Modified    time.Time
	Title       string
}

const windowsBootImageSelect = `SELECT id,source_kind,COALESCE(windows_profile_id,0),COALESCE(storage_id,0),path,storage_root,size_bytes,modified_at,title,sha256,has_setup_exe,wim_info,state,last_error,created_at FROM windows_boot_images`

const windowsInstallImageSelect = `SELECT id,storage_id,path,storage_root,install_file,size_bytes,modified_at,title,wim_info,state,last_error,created_at FROM windows_install_images`

func scanWindowsBootImage(row pgx.Row) (WindowsBootImage, error) {
	var img WindowsBootImage
	err := row.Scan(
		&img.ID, &img.SourceKind, &img.WindowsProfileID, &img.StorageID,
		&img.Path, &img.StorageRoot, &img.Size, &img.Modified,
		&img.Title, &img.SHA256, &img.HasSetupExe, &img.WIMInfo,
		&img.State, &img.Error, &img.CreatedAt,
	)
	return img, err
}

func scanWindowsInstallImage(row pgx.Row) (WindowsInstallImage, error) {
	var img WindowsInstallImage
	err := row.Scan(
		&img.ID, &img.StorageID, &img.Path, &img.StorageRoot,
		&img.InstallFile, &img.Size, &img.Modified, &img.Title,
		&img.WIMInfo, &img.State, &img.Error, &img.CreatedAt,
	)
	return img, err
}

func (s Store) ListWindowsBootImages(ctx context.Context) ([]WindowsBootImage, error) {
	rows, err := s.Pool.Query(ctx, windowsBootImageSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WindowsBootImage{}
	for rows.Next() {
		img, err := scanWindowsBootImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

func (s Store) GetWindowsBootImage(ctx context.Context, id int64) (WindowsBootImage, error) {
	return scanWindowsBootImage(s.Pool.QueryRow(ctx, windowsBootImageSelect+` WHERE id=$1`, id))
}

func (s Store) UpsertISOBootImage(ctx context.Context, windowsProfileID int64, title, sha256 string, hasSetupExe bool, wimInfo agent.WindowsWIMInfo, state string) (WindowsBootImage, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WindowsBootImage{}, err
	}
	defer tx.Rollback(ctx)
	img, err := upsertISOBootImageTx(ctx, tx, windowsProfileID, title, sha256, hasSetupExe, wimInfo, state)
	if err != nil {
		return WindowsBootImage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WindowsBootImage{}, err
	}
	return img, nil
}

func upsertISOBootImageTx(ctx context.Context, tx pgx.Tx, windowsProfileID int64, title, sha256 string, hasSetupExe bool, wimInfo agent.WindowsWIMInfo, state string) (WindowsBootImage, error) {
	if windowsProfileID <= 0 {
		return WindowsBootImage{}, fmt.Errorf("invalid Windows profile ID")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = fmt.Sprintf("Windows ISO #%d", windowsProfileID)
	}
	if state == "" {
		state = "ready"
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO windows_boot_images (source_kind, windows_profile_id, title, sha256, has_setup_exe, wim_info, state, last_error)
		VALUES ('iso', $1, $2, $3, $4, $5, $6, '')
		ON CONFLICT (windows_profile_id) DO UPDATE SET
			title = EXCLUDED.title,
			sha256 = CASE WHEN EXCLUDED.sha256 <> '' THEN EXCLUDED.sha256 ELSE windows_boot_images.sha256 END,
			has_setup_exe = EXCLUDED.has_setup_exe,
			wim_info = CASE WHEN EXCLUDED.wim_info <> '{}'::jsonb THEN EXCLUDED.wim_info ELSE windows_boot_images.wim_info END,
			state = EXCLUDED.state,
			last_error = ''
		RETURNING id,source_kind,COALESCE(windows_profile_id,0),COALESCE(storage_id,0),path,storage_root,size_bytes,modified_at,title,sha256,has_setup_exe,wim_info,state,last_error,created_at`,
		windowsProfileID, title, sha256, hasSetupExe, wimInfo, state,
	)
	return scanWindowsBootImage(row)
}

func (s Store) UpsertFileBootImage(ctx context.Context, src WindowsBootImageSource) (WindowsBootImage, error) {
	title := strings.TrimSpace(src.Title)
	if src.StorageID <= 0 || strings.TrimSpace(src.Path) == "" || title == "" {
		return WindowsBootImage{}, fmt.Errorf("invalid Windows boot image source")
	}
	var modified *time.Time
	if !src.Modified.IsZero() {
		t := src.Modified.UTC()
		modified = &t
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO windows_boot_images (source_kind, storage_id, path, storage_root, size_bytes, modified_at, title, state, last_error)
		VALUES ('file', $1, $2, $3, $4, $5, $6, 'pending', '')
		ON CONFLICT (storage_id, path) WHERE source_kind = 'file' DO UPDATE SET
			storage_root = EXCLUDED.storage_root,
			size_bytes = EXCLUDED.size_bytes,
			modified_at = EXCLUDED.modified_at,
			title = EXCLUDED.title,
			state = 'pending',
			last_error = ''
		RETURNING id,source_kind,COALESCE(windows_profile_id,0),COALESCE(storage_id,0),path,storage_root,size_bytes,modified_at,title,sha256,has_setup_exe,wim_info,state,last_error,created_at`,
		src.StorageID, src.Path, src.StorageRoot, src.Size, modified, title,
	)
	return scanWindowsBootImage(row)
}

func (s Store) SetBootImageReady(ctx context.Context, id int64, sha256 string, hasSetupExe bool, wimInfo agent.WindowsWIMInfo) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE windows_boot_images SET sha256=$2, has_setup_exe=$3, wim_info=$4, state='ready', last_error='' WHERE id=$1`,
		id, sha256, hasSetupExe, wimInfo,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SetBootImageError(ctx context.Context, id int64, state, lastError string) error {
	if state == "" {
		state = "error"
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE windows_boot_images SET state=$2, last_error=$3 WHERE id=$1`,
		id, state, lastError,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SetBootImageTitle(ctx context.Context, id int64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("boot image title is empty")
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE windows_boot_images SET title=$2 WHERE id=$1`, id, title)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) DeleteWindowsBootImage(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM windows_boot_images WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) ListWindowsInstallImages(ctx context.Context) ([]WindowsInstallImage, error) {
	rows, err := s.Pool.Query(ctx, windowsInstallImageSelect+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WindowsInstallImage{}
	for rows.Next() {
		img, err := scanWindowsInstallImage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

func (s Store) GetWindowsInstallImage(ctx context.Context, id int64) (WindowsInstallImage, error) {
	return scanWindowsInstallImage(s.Pool.QueryRow(ctx, windowsInstallImageSelect+` WHERE id=$1`, id))
}

func (s Store) UpsertWindowsInstallImage(ctx context.Context, src WindowsInstallImageSource) (WindowsInstallImage, error) {
	title := strings.TrimSpace(src.Title)
	if src.StorageID <= 0 || strings.TrimSpace(src.Path) == "" || src.Size <= 0 || title == "" {
		return WindowsInstallImage{}, fmt.Errorf("invalid Windows install image source")
	}
	installFile := src.InstallFile
	if installFile == "" {
		if strings.EqualFold(path.Ext(src.Path), ".esd") {
			installFile = "install.esd"
		} else {
			installFile = "install.wim"
		}
	}
	modified := src.Modified.UTC()
	if modified.IsZero() {
		modified = time.Now().UTC()
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WindowsInstallImage{}, err
	}
	defer tx.Rollback(ctx)

	img, err := scanWindowsInstallImage(tx.QueryRow(ctx, `
		INSERT INTO windows_install_images (storage_id, path, storage_root, install_file, size_bytes, modified_at, title, state, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', '')
		ON CONFLICT (storage_id, path) DO UPDATE SET
			storage_root = EXCLUDED.storage_root,
			install_file = EXCLUDED.install_file,
			size_bytes = EXCLUDED.size_bytes,
			modified_at = EXCLUDED.modified_at,
			title = EXCLUDED.title,
			state = 'pending',
			last_error = ''
		RETURNING id,storage_id,path,storage_root,install_file,size_bytes,modified_at,title,wim_info,state,last_error,created_at`,
		src.StorageID, src.Path, src.StorageRoot, installFile, src.Size, modified, title,
	))
	if err != nil {
		return WindowsInstallImage{}, err
	}

	var bootProfileID int64
	err = tx.QueryRow(ctx, `SELECT boot_profile_id FROM windows_install_boot_profiles WHERE install_image_id=$1`, img.ID).Scan(&bootProfileID)
	if err == pgx.ErrNoRows {
		if err := createWindowsInstallBootProfile(ctx, tx, img); err != nil {
			return WindowsInstallImage{}, err
		}
	} else if err != nil {
		return WindowsInstallImage{}, err
	} else {
		if _, err := tx.Exec(ctx, `UPDATE boot_profiles SET title=$2 WHERE id=$1`, bootProfileID, img.Title); err != nil {
			return WindowsInstallImage{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return WindowsInstallImage{}, err
	}
	return img, nil
}

func (s Store) SetInstallImageReady(ctx context.Context, id int64, installFile string, wimInfo agent.WindowsWIMInfo) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE windows_install_images SET install_file=COALESCE(NULLIF($2,''), install_file), wim_info=$3, state='ready', last_error='' WHERE id=$1`,
		id, installFile, wimInfo,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SetInstallImageError(ctx context.Context, id int64, state, lastError string) error {
	if state == "" {
		state = "error"
	}
	tag, err := s.Pool.Exec(ctx,
		`UPDATE windows_install_images SET state=$2, last_error=$3 WHERE id=$1`,
		id, state, lastError,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SetInstallImageTitle(ctx context.Context, id int64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("install image title is empty")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `UPDATE windows_install_images SET title=$2 WHERE id=$1`, id, title)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx,
		`UPDATE boot_profiles b SET title=$2 FROM windows_install_boot_profiles w WHERE b.id=w.boot_profile_id AND w.install_image_id=$1`,
		id, title,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) DeleteWindowsInstallImage(ctx context.Context, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var existing int64
	if err := tx.QueryRow(ctx, `SELECT id FROM windows_install_images WHERE id=$1 FOR UPDATE`, id).Scan(&existing); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM boot_profiles b USING windows_install_boot_profiles w WHERE b.id=w.boot_profile_id AND w.install_image_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM windows_install_images WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RebindWIMImageStorage updates storage_id and storage_root for any standalone
// boot.wim or install.wim/.esd record moved between storages during migration.
func (s Store) RebindWIMImageStorage(ctx context.Context, srcStorageID int64, relPath string, dstStorageID int64, dstRoot string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE windows_boot_images SET storage_id=$1, storage_root=$2 WHERE source_kind='file' AND storage_id=$3 AND path=$4`,
		dstStorageID, dstRoot, srcStorageID, relPath,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE windows_install_images SET storage_id=$1, storage_root=$2 WHERE storage_id=$3 AND path=$4`,
		dstStorageID, dstRoot, srcStorageID, relPath,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
