package control

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s Store) CreateWindowsProfile(ctx context.Context, source WindowsProfileSource) (WindowsProfile, error) {
	profile := WindowsProfile{WindowsProfileSource: source}
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO windows_profiles (storage_id,path,storage_root,size_bytes,modified_at,title,folder,state)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending')
		 RETURNING id,state`,
		source.StorageID, source.Path, source.StorageRoot, source.Size, source.Modified, source.Title, source.Folder,
	).Scan(&profile.ID, &profile.State)
	return profile, err
}

func (s Store) WindowsProfiles(ctx context.Context) ([]WindowsProfile, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id,storage_id,path,storage_root,size_bytes,modified_at,title,folder,install_file,state,last_error
		 FROM windows_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []WindowsProfile{}
	for rows.Next() {
		var p WindowsProfile
		if err := rows.Scan(&p.ID, &p.StorageID, &p.Path, &p.StorageRoot, &p.Size, &p.Modified, &p.Title, &p.Folder, &p.InstallFile, &p.State, &p.Error); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (s Store) WindowsProfile(ctx context.Context, id int64) (WindowsProfile, error) {
	var p WindowsProfile
	err := s.Pool.QueryRow(ctx,
		`SELECT id,storage_id,path,storage_root,size_bytes,modified_at,title,folder,install_file,state,last_error
		 FROM windows_profiles WHERE id=$1`, id,
	).Scan(&p.ID, &p.StorageID, &p.Path, &p.StorageRoot, &p.Size, &p.Modified, &p.Title, &p.Folder, &p.InstallFile, &p.State, &p.Error)
	return p, err
}

func (s Store) SetWindowsProfileResult(ctx context.Context, id int64, state, installFile, lastError string) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE windows_profiles SET state=$2,install_file=$3,last_error=$4 WHERE id=$1`, id, state, installFile, lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) DeleteWindowsProfile(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM windows_profiles WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
