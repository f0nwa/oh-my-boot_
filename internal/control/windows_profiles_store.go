package control

import (
	"context"

	"omb/internal/agent"

	"github.com/jackc/pgx/v5"
)

func (s Store) CreateWindowsProfile(ctx context.Context, source WindowsProfileSource) (WindowsProfile, error) {
	profile := WindowsProfile{WindowsProfileSource: source}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WindowsProfile{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx,
		`INSERT INTO windows_profiles (storage_id,path,storage_root,size_bytes,modified_at,title,folder,state,boot_mode)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8)
		 RETURNING id,state`,
		source.StorageID, source.Path, source.StorageRoot, source.Size, source.Modified, source.Title, source.Folder, source.BootMode,
	).Scan(&profile.ID, &profile.State)
	if err != nil {
		return WindowsProfile{}, err
	}
	if err := createWindowsBootProfile(ctx, tx, profile); err != nil {
		return WindowsProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WindowsProfile{}, err
	}
	return profile, nil
}

func (s Store) WindowsProfiles(ctx context.Context) ([]WindowsProfile, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id,storage_id,path,storage_root,size_bytes,modified_at,title,folder,install_file,state,last_error,boot_mode,wim_info
		 FROM windows_profiles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []WindowsProfile{}
	for rows.Next() {
		var p WindowsProfile
		if err := rows.Scan(&p.ID, &p.StorageID, &p.Path, &p.StorageRoot, &p.Size, &p.Modified, &p.Title, &p.Folder, &p.InstallFile, &p.State, &p.Error, &p.BootMode, &p.WIMInfo); err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

func (s Store) WindowsProfile(ctx context.Context, id int64) (WindowsProfile, error) {
	var p WindowsProfile
	err := s.Pool.QueryRow(ctx,
		`SELECT id,storage_id,path,storage_root,size_bytes,modified_at,title,folder,install_file,state,last_error,boot_mode,wim_info
		 FROM windows_profiles WHERE id=$1`, id,
	).Scan(&p.ID, &p.StorageID, &p.Path, &p.StorageRoot, &p.Size, &p.Modified, &p.Title, &p.Folder, &p.InstallFile, &p.State, &p.Error, &p.BootMode, &p.WIMInfo)
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

func (s Store) SetWindowsProfileWIMInfo(ctx context.Context, id int64, info agent.WindowsWIMInfo) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE windows_profiles SET wim_info=$2 WHERE id=$1`, id, info)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DeleteWindowsProfile removes the ISO and its common profile together. The
// common profile's foreign keys reject deletion while installations use it;
// library references to the ISO likewise roll the whole transaction back.
func (s Store) DeleteWindowsProfile(ctx context.Context, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var profileID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM windows_profiles WHERE id=$1 FOR UPDATE`, id).Scan(&profileID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM boot_profiles b USING windows_boot_profiles w WHERE b.id=w.boot_profile_id AND w.windows_profile_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM windows_profiles WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
