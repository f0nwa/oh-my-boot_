package images

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"omb/internal/nfsstorage"
	"omb/internal/smbstorage"
)

var (
	ErrStorageNameTaken   = errors.New("storage name is already in use")
	ErrInvalidStorageName = errors.New("invalid storage name")
)

type Catalog struct {
	Storages []Storage `json:"storages"`
	Images   []Image   `json:"images"`
}

type Store struct{ Pool *pgxpool.Pool }

type ManagedStorageInput struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	Server  string `json:"server"`
	Share   string `json:"share"`
	Subpath string `json:"subpath"`
}

type NFSStorageInput struct {
	Name       string `json:"name"`
	Key        string `json:"key"`
	Server     string `json:"server"`
	ExportPath string `json:"export_path"`
	Version    string `json:"version"`
}

func (s Store) CreateSMB(ctx context.Context, input ManagedStorageInput) (Storage, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 {
		return Storage{}, fmt.Errorf("invalid storage name")
	}
	if err := smbstorage.Validate(input.Key, input.Server, input.Share, input.Subpath); err != nil {
		return Storage{}, err
	}
	var storage Storage
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO storages (name,kind,root,storage_key,server,share,subpath,state,last_error)
		 VALUES ($1,'smb',$2,$3,$4,$5,$6,'unavailable','Ожидает подключения')
		 RETURNING id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,export_path,nfs_version,uploads_enabled,state,last_error,last_scan_at,other_bytes`,
		input.Name, smbstorage.Root(input.Key), input.Key, input.Server, input.Share, input.Subpath,
	).Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.ExportPath, &storage.NFSVersion, &storage.UploadsEnabled, &storage.State, &storage.Error, &storage.LastScanAt, &storage.OtherBytes)
	return storage, err
}

func (s Store) DeleteSMB(ctx context.Context, id int64) error {
	command, err := s.Pool.Exec(ctx, "DELETE FROM storages WHERE id=$1 AND kind='smb'", id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) CreateNFS(ctx context.Context, input NFSStorageInput) (Storage, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 {
		return Storage{}, fmt.Errorf("invalid storage name")
	}
	if err := nfsstorage.Validate(input.Key, input.Server, input.ExportPath, input.Version); err != nil {
		return Storage{}, err
	}
	var storage Storage
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO storages (name,kind,root,storage_key,server,export_path,nfs_version,uploads_enabled,state,last_error)
		 VALUES ($1,'nfs',$2,$3,$4,$5,$6,FALSE,'unavailable','Ожидает подключения')
		 RETURNING id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,export_path,nfs_version,uploads_enabled,state,last_error,last_scan_at,other_bytes`,
		input.Name, nfsstorage.Root(input.Key), input.Key, input.Server, input.ExportPath, input.Version,
	).Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.ExportPath, &storage.NFSVersion, &storage.UploadsEnabled, &storage.State, &storage.Error, &storage.LastScanAt, &storage.OtherBytes)
	return storage, err
}

func (s Store) UpdateNFSConfig(ctx context.Context, id int64, version string) error {
	if version != "4.1" && version != "3" {
		return fmt.Errorf("invalid NFS version: %q", version)
	}
	command, err := s.Pool.Exec(ctx, "UPDATE storages SET nfs_version=$2 WHERE id=$1 AND kind='nfs'", id, version)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SetUploadsEnabled(ctx context.Context, id int64, enabled bool) error {
	command, err := s.Pool.Exec(ctx, "UPDATE storages SET uploads_enabled=$2 WHERE id=$1 AND kind='nfs'", id, enabled)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) DeleteNFS(ctx context.Context, id int64) error {
	command, err := s.Pool.Exec(ctx, "DELETE FROM storages WHERE id=$1 AND kind='nfs'", id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) RenameStorage(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 120 {
		return ErrInvalidStorageName
	}
	command, err := s.Pool.Exec(ctx, "UPDATE storages SET name=$2 WHERE id=$1", id, name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrStorageNameTaken
		}
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "storages_name_key") {
			return ErrStorageNameTaken
		}
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// RecoverInterruptedScans marks jobs left in progress by a previous process as failed.
func (s Store) RecoverInterruptedScans(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, "UPDATE storages SET state='unavailable',last_error='Сканирование прервано перезапуском службы' WHERE state='scanning'")
	return err
}

func (s Store) AddStorage(ctx context.Context, name, kind, root string) (Storage, error) {
	name = strings.TrimSpace(name)
	var err error
	root, err = ValidateStorage(name, kind, root)
	if err != nil {
		return Storage{}, err
	}
	var storage Storage
	err = s.Pool.QueryRow(ctx,
		"INSERT INTO storages (name,kind,root) VALUES ($1,$2,$3) RETURNING id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,export_path,nfs_version,uploads_enabled,state,last_error,last_scan_at,other_bytes",
		name, kind, root,
	).Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.ExportPath, &storage.NFSVersion, &storage.UploadsEnabled, &storage.State, &storage.Error, &storage.LastScanAt, &storage.OtherBytes)
	return storage, err
}

func (s Store) Storage(ctx context.Context, id int64) (Storage, error) {
	var storage Storage
	err := s.Pool.QueryRow(ctx, "SELECT id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,export_path,nfs_version,uploads_enabled,state,last_error,last_scan_at,other_bytes FROM storages WHERE id=$1", id).
		Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.ExportPath, &storage.NFSVersion, &storage.UploadsEnabled, &storage.State, &storage.Error, &storage.LastScanAt, &storage.OtherBytes)
	return storage, err
}

func (s Store) Catalog(ctx context.Context) (Catalog, error) {
	result := Catalog{Storages: []Storage{}, Images: []Image{}}
	rows, err := s.Pool.Query(ctx, "SELECT id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,export_path,nfs_version,uploads_enabled,state,last_error,last_scan_at,other_bytes FROM storages ORDER BY name")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var storage Storage
		if err := rows.Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.ExportPath, &storage.NFSVersion, &storage.UploadsEnabled, &storage.State, &storage.Error, &storage.LastScanAt, &storage.OtherBytes); err != nil {
			rows.Close()
			return result, err
		}
		result.Storages = append(result.Storages, storage)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	rows, err = s.Pool.Query(ctx, "SELECT storage_id,path,size_bytes,modified_at,present,first_seen_at FROM images ORDER BY storage_id,path")
	if err != nil {
		return result, err
	}
	defer rows.Close()
	isoBytes := map[int64]int64{}
	for rows.Next() {
		var image Image
		if err := rows.Scan(&image.StorageID, &image.Path, &image.Size, &image.Modified, &image.Present, &image.FirstSeenAt); err != nil {
			return result, err
		}
		if image.Present {
			isoBytes[image.StorageID] += image.Size
		}
		result.Images = append(result.Images, image)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	for i := range result.Storages {
		result.Storages[i].ISOBytes = isoBytes[result.Storages[i].ID]
	}
	return result, nil
}

func (s Store) MarkScanning(ctx context.Context, id int64) error {
	command, err := s.Pool.Exec(ctx, "UPDATE storages SET state='scanning',last_error='' WHERE id=$1", id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SaveScan(ctx context.Context, id int64, found []Image, otherBytes int64) error {
	if otherBytes < 0 {
		otherBytes = 0
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "UPDATE images SET present=FALSE WHERE storage_id=$1", id); err != nil {
		return err
	}
	for _, image := range found {
		if _, err := tx.Exec(ctx,
			"INSERT INTO images (storage_id,path,size_bytes,modified_at,present) VALUES ($1,$2,$3,$4,TRUE) ON CONFLICT (storage_id,path) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,modified_at=EXCLUDED.modified_at,present=TRUE",
			id, image.Path, image.Size, image.Modified,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE storages SET state='ready',last_error='',last_scan_at=now(),other_bytes=$2 WHERE id=$1", id, otherBytes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) RecordUpload(ctx context.Context, image Image) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO images (storage_id,path,size_bytes,modified_at,present) VALUES ($1,$2,$3,$4,TRUE) ON CONFLICT (storage_id,path) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,modified_at=EXCLUDED.modified_at,present=TRUE",
		image.StorageID, image.Path, image.Size, image.Modified,
	)
	return err
}

func (s Store) DeleteImage(ctx context.Context, id int64, path string) error {
	command, err := s.Pool.Exec(ctx, "DELETE FROM images WHERE storage_id=$1 AND path=$2", id, path)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) ScanError(ctx context.Context, id int64, message string) error {
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := s.Pool.Exec(ctx, "UPDATE storages SET state='unavailable',last_error=$2 WHERE id=$1", id, message)
	return err
}

// Storages lists storages without their image catalog.
func (s Store) Storages(ctx context.Context) ([]Storage, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,export_path,nfs_version,uploads_enabled,state,last_error,last_scan_at,other_bytes FROM storages ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Storage{}
	for rows.Next() {
		var storage Storage
		if err := rows.Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.ExportPath, &storage.NFSVersion, &storage.UploadsEnabled, &storage.State, &storage.Error, &storage.LastScanAt, &storage.OtherBytes); err != nil {
			return nil, err
		}
		result = append(result, storage)
	}
	return result, rows.Err()
}

// MarkUnavailableIfReady records a failed availability check without
// overriding a scan that started in the meantime.
func (s Store) MarkUnavailableIfReady(ctx context.Context, id int64, message string) (bool, error) {
	if len(message) > 500 {
		message = message[:500]
	}
	command, err := s.Pool.Exec(ctx, "UPDATE storages SET state='unavailable',last_error=$2 WHERE id=$1 AND state='ready'", id, message)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() > 0, nil
}
