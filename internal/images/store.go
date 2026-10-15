package images

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"pxe-platform/internal/smbstorage"
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
		 RETURNING id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,state,last_error,last_scan_at`,
		input.Name, smbstorage.Root(input.Key), input.Key, input.Server, input.Share, input.Subpath,
	).Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.State, &storage.Error, &storage.LastScanAt)
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
		"INSERT INTO storages (name,kind,root) VALUES ($1,$2,$3) RETURNING id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,state,last_error,last_scan_at",
		name, kind, root,
	).Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.State, &storage.Error, &storage.LastScanAt)
	return storage, err
}

func (s Store) Storage(ctx context.Context, id int64) (Storage, error) {
	var storage Storage
	err := s.Pool.QueryRow(ctx, "SELECT id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,state,last_error,last_scan_at FROM storages WHERE id=$1", id).
		Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.State, &storage.Error, &storage.LastScanAt)
	return storage, err
}

func (s Store) Catalog(ctx context.Context) (Catalog, error) {
	result := Catalog{Storages: []Storage{}, Images: []Image{}}
	rows, err := s.Pool.Query(ctx, "SELECT id,name,kind,root,COALESCE(storage_key,''),server,share,subpath,state,last_error,last_scan_at FROM storages ORDER BY name")
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var storage Storage
		if err := rows.Scan(&storage.ID, &storage.Name, &storage.Kind, &storage.Root, &storage.Key, &storage.Server, &storage.Share, &storage.Subpath, &storage.State, &storage.Error, &storage.LastScanAt); err != nil {
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
	rows, err = s.Pool.Query(ctx, "SELECT storage_id,path,size_bytes,modified_at,present FROM images ORDER BY storage_id,path")
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var image Image
		if err := rows.Scan(&image.StorageID, &image.Path, &image.Size, &image.Modified, &image.Present); err != nil {
			return result, err
		}
		result.Images = append(result.Images, image)
	}
	return result, rows.Err()
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

func (s Store) SaveScan(ctx context.Context, id int64, found []Image) error {
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
	if _, err := tx.Exec(ctx, "UPDATE storages SET state='ready',last_error='',last_scan_at=now() WHERE id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) ScanError(ctx context.Context, id int64, message string) error {
	if len(message) > 500 {
		message = message[:500]
	}
	_, err := s.Pool.Exec(ctx, "UPDATE storages SET state='unavailable',last_error=$2 WHERE id=$1", id, message)
	return err
}
