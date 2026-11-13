package control

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s Store) CreateWindowsAnswer(ctx context.Context, variant WindowsAnswerVariant) (WindowsAnswerVariant, error) {
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO windows_answer_variants (profile_id,title,folder,sha256,size_bytes,state,wipes_disk,disk_summary)
		 VALUES ($1,$2,$3,$4,$5,'pending',$6,$7) RETURNING id,state,created_at`,
		variant.ProfileID, variant.Title, variant.Folder, variant.SHA256, variant.Size, variant.WipesDisk, variant.Disk,
	).Scan(&variant.ID, &variant.State, &variant.CreatedAt)
	return variant, err
}

func (s Store) SetWindowsAnswerState(ctx context.Context, id int64, state, lastError string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE windows_answer_variants SET state=$2,last_error=$3 WHERE id=$1`, id, state, lastError)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) WindowsAnswers(ctx context.Context) ([]WindowsAnswerVariant, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id,profile_id,title,folder,sha256,size_bytes,state,last_error,wipes_disk,created_at,disk_summary FROM windows_answer_variants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	variants := []WindowsAnswerVariant{}
	for rows.Next() {
		var v WindowsAnswerVariant
		if err := rows.Scan(&v.ID, &v.ProfileID, &v.Title, &v.Folder, &v.SHA256, &v.Size, &v.State, &v.Error, &v.WipesDisk, &v.CreatedAt, &v.Disk); err != nil {
			return nil, err
		}
		variants = append(variants, v)
	}
	return variants, rows.Err()
}

func (s Store) WindowsAnswer(ctx context.Context, id int64) (WindowsAnswerVariant, error) {
	var v WindowsAnswerVariant
	err := s.Pool.QueryRow(ctx,
		`SELECT id,profile_id,title,folder,sha256,size_bytes,state,last_error,wipes_disk,created_at,disk_summary FROM windows_answer_variants WHERE id=$1`, id,
	).Scan(&v.ID, &v.ProfileID, &v.Title, &v.Folder, &v.SHA256, &v.Size, &v.State, &v.Error, &v.WipesDisk, &v.CreatedAt, &v.Disk)
	return v, err
}

func (s Store) DeleteWindowsAnswer(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM windows_answer_variants WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s Store) SetWindowsAnswerWipesDisk(ctx context.Context, id int64, wipes bool) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE windows_answer_variants SET wipes_disk=$2 WHERE id=$1`, id, wipes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
