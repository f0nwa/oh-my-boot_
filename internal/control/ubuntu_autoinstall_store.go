package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"omb/internal/autoinstall"
)

// CreateUbuntuAutoinstall persists a validated constructor result or uploaded YAML file.
// The stored bytes and metadata are immutable; only the display title can be renamed.
func (s Store) CreateUbuntuAutoinstall(ctx context.Context, file UbuntuAutoinstallFile) (UbuntuAutoinstallFile, error) {
	if !safeMenuText(file.Title, 80) {
		return UbuntuAutoinstallFile{}, fmt.Errorf("invalid Autoinstall title")
	}
	if file.State != "validated" {
		return UbuntuAutoinstallFile{}, fmt.Errorf("Autoinstall file must be validated")
	}
	if file.SourceKind == "" {
		file.SourceKind = "editor"
	}
	var values []byte
	var err error
	switch file.SourceKind {
	case "editor":
		rendered, renderErr := autoinstall.Render(file.Values)
		if renderErr != nil {
			return UbuntuAutoinstallFile{}, renderErr
		}
		if !bytes.Equal(rendered, file.UserData) || fmt.Sprintf("%x", sha256.Sum256(file.UserData)) != file.SHA256 {
			return UbuntuAutoinstallFile{}, fmt.Errorf("Autoinstall content does not match its values and digest")
		}
		values, err = json.Marshal(file.Values)
		if err != nil {
			return UbuntuAutoinstallFile{}, err
		}
		file.DiskMode = file.Values.DiskMode
		file.DiskSerial = file.Values.DiskSerial
	case "uploaded":
		clean, meta, valErr := autoinstall.ValidateUploadedUserData(file.UserData)
		if valErr != nil {
			return UbuntuAutoinstallFile{}, valErr
		}
		if !bytes.Equal(clean, file.UserData) || fmt.Sprintf("%x", sha256.Sum256(file.UserData)) != file.SHA256 {
			return UbuntuAutoinstallFile{}, fmt.Errorf("Autoinstall content does not match its digest")
		}
		values = []byte("{}")
		file.Values = autoinstall.Values{}
		file.DiskMode = meta.DiskMode
		file.DiskSerial = meta.DiskSerial
	default:
		return UbuntuAutoinstallFile{}, fmt.Errorf("invalid Autoinstall source kind")
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO ubuntu_autoinstall_files(title,source_kind,"values",user_data,sha256,state,disk_mode,disk_serial) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,created_at`, file.Title, file.SourceKind, values, file.UserData, file.SHA256, file.State, file.DiskMode, file.DiskSerial).Scan(&file.ID, &file.CreatedAt)
	if err != nil {
		return UbuntuAutoinstallFile{}, err
	}
	return file, nil
}

func (s Store) UbuntuAutoinstalls(ctx context.Context) ([]UbuntuAutoinstallSummary, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,title,source_kind,sha256,state,disk_mode,disk_serial,created_at FROM ubuntu_autoinstall_files ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []UbuntuAutoinstallSummary{}
	for rows.Next() {
		var file UbuntuAutoinstallSummary
		if err := rows.Scan(&file.ID, &file.Title, &file.SourceKind, &file.SHA256, &file.State, &file.DiskMode, &file.DiskSerial, &file.CreatedAt); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func (s Store) UbuntuAutoinstall(ctx context.Context, id int64) (UbuntuAutoinstallFile, error) {
	var file UbuntuAutoinstallFile
	var values []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,title,source_kind,"values",user_data,sha256,state,disk_mode,disk_serial,created_at FROM ubuntu_autoinstall_files WHERE id=$1`, id).Scan(&file.ID, &file.Title, &file.SourceKind, &values, &file.UserData, &file.SHA256, &file.State, &file.DiskMode, &file.DiskSerial, &file.CreatedAt)
	if err != nil {
		return UbuntuAutoinstallFile{}, err
	}
	if file.SourceKind != "uploaded" {
		if err := json.Unmarshal(values, &file.Values); err != nil {
			return UbuntuAutoinstallFile{}, err
		}
	}
	return file, nil
}

func (s Store) SetUbuntuAutoinstallTitle(ctx context.Context, id int64, title string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE ubuntu_autoinstall_files SET title=$2 WHERE id=$1`, id, title)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) DeleteUbuntuAutoinstall(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM ubuntu_autoinstall_files WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
