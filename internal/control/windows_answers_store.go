package control

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func (s Store) CreateWindowsAnswer(ctx context.Context, variant WindowsAnswerVariant) (WindowsAnswerVariant, error) {
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO windows_answer_variants (title,folder,sha256,size_bytes,state,wipes_disk,disk_summary,target_summary)
		 VALUES ($1,$2,$3,$4,'pending',$5,$6,$7) RETURNING id,state,created_at`,
		variant.Title, variant.Folder, variant.SHA256, variant.Size, variant.WipesDisk, variant.Disk, variant.Target,
	).Scan(&variant.ID, &variant.State, &variant.CreatedAt)
	return variant, err
}

func (s Store) UpdateWindowsAnswer(ctx context.Context, variant WindowsAnswerVariant) (WindowsAnswerVariant, error) {
	err := s.Pool.QueryRow(ctx,
		`UPDATE windows_answer_variants
		 SET title=$2,sha256=$3,size_bytes=$4,state='ready',last_error='',wipes_disk=$5,disk_summary=$6,target_summary=$7
		 WHERE id=$1
		 RETURNING id,title,folder,sha256,size_bytes,state,last_error,wipes_disk,created_at,disk_summary,target_summary`,
		variant.ID, variant.Title, variant.SHA256, variant.Size, variant.WipesDisk, variant.Disk, variant.Target,
	).Scan(&variant.ID, &variant.Title, &variant.Folder, &variant.SHA256, &variant.Size, &variant.State, &variant.Error, &variant.WipesDisk, &variant.CreatedAt, &variant.Disk, &variant.Target)
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
		`SELECT id,title,folder,sha256,size_bytes,state,last_error,wipes_disk,created_at,disk_summary,target_summary FROM windows_answer_variants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	variants := []WindowsAnswerVariant{}
	for rows.Next() {
		var v WindowsAnswerVariant
		if err := rows.Scan(&v.ID, &v.Title, &v.Folder, &v.SHA256, &v.Size, &v.State, &v.Error, &v.WipesDisk, &v.CreatedAt, &v.Disk, &v.Target); err != nil {
			return nil, err
		}
		variants = append(variants, v)
	}
	return variants, rows.Err()
}

func (s Store) WindowsAnswer(ctx context.Context, id int64) (WindowsAnswerVariant, error) {
	var v WindowsAnswerVariant
	err := s.Pool.QueryRow(ctx,
		`SELECT id,title,folder,sha256,size_bytes,state,last_error,wipes_disk,created_at,disk_summary,target_summary FROM windows_answer_variants WHERE id=$1`, id,
	).Scan(&v.ID, &v.Title, &v.Folder, &v.SHA256, &v.Size, &v.State, &v.Error, &v.WipesDisk, &v.CreatedAt, &v.Disk, &v.Target)
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

func (s Store) SetWindowsAnswerTitle(ctx context.Context, id int64, title string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE windows_answer_variants SET title=$2 WHERE id=$1`, id, title)
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

func (s Store) SetWindowsAnswerTarget(ctx context.Context, id int64, target WindowsAnswerTarget) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE windows_answer_variants SET target_summary=$2 WHERE id=$1`, id, target)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
