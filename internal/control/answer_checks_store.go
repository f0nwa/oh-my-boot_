package control

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s Store) SaveAnswerCheck(ctx context.Context, input AnswerCheck) error {
	c := normalizedAnswerCheck(input)
	if !validAnswerCheck(c) {
		return fmt.Errorf("invalid installation check")
	}
	column, _ := checkFileColumn(c.Kind)
	table, ready := "windows_answer_variants", "ready"
	if c.Kind == "ubuntu" {
		table, ready = "ubuntu_autoinstall_files", "validated"
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var digest, state string
	if err := tx.QueryRow(ctx, `SELECT sha256,state FROM `+table+` WHERE id=$1 FOR SHARE`, c.AnswerID).Scan(&digest, &state); err != nil {
		return err
	}
	if digest != c.SHA256 || state != ready {
		return errAnswerCheckConflict
	}
	profileID := c.WindowsProfileID
	profileTitle := c.ProfileTitle
	if c.Kind == "windows" {
		if profileID > 0 {
			if err := tx.QueryRow(ctx, `SELECT title FROM windows_profiles WHERE id=$1 FOR SHARE`, profileID).Scan(&profileTitle); err != nil {
				return err
			}
		}
	} else {
		profileID = 0
		if profileTitle == "" {
			profileTitle = "Ubuntu Desktop"
		}
	}
	var author string
	if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id=$1 AND enabled AND role IN ('admin','operator') FOR SHARE`, c.UserID).Scan(&author); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO answer_installation_checks (`+column+`,windows_profile_id,profile_title,sha256,result,machine,comment,user_id,author) VALUES($1,NULLIF($2,0),$3,$4,$5,$6,$7,$8,$9)`, c.AnswerID, profileID, profileTitle, digest, c.Result, c.Machine, c.Comment, c.UserID, author); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const answerCheckFields = `id,CASE WHEN windows_answer_id IS NOT NULL THEN 'windows' ELSE 'ubuntu' END,COALESCE(windows_answer_id,ubuntu_autoinstall_id),COALESCE(windows_profile_id,0),profile_title,sha256,result,machine,comment,COALESCE(user_id,0),author,created_at`

func scanAnswerChecks(rows pgx.Rows) ([]AnswerCheck, error) {
	defer rows.Close()
	checks := []AnswerCheck{}
	for rows.Next() {
		var c AnswerCheck
		if err := rows.Scan(&c.ID, &c.Kind, &c.AnswerID, &c.WindowsProfileID, &c.ProfileTitle, &c.SHA256, &c.Result, &c.Machine, &c.Comment, &c.UserID, &c.Author, &c.CreatedAt); err != nil {
			return nil, err
		}
		checks = append(checks, c)
	}
	return checks, rows.Err()
}

func (s Store) LatestAnswerChecks(ctx context.Context) ([]AnswerCheck, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (COALESCE(windows_answer_id,0),COALESCE(ubuntu_autoinstall_id,0)) `+answerCheckFields+` FROM answer_installation_checks ORDER BY COALESCE(windows_answer_id,0),COALESCE(ubuntu_autoinstall_id,0),id DESC`)
	if err != nil {
		return nil, err
	}
	return scanAnswerChecks(rows)
}

func (s Store) LatestAnswerChecksByProfile(ctx context.Context) ([]AnswerCheck, error) {
	rows, err := s.Pool.Query(ctx, `SELECT * FROM (SELECT DISTINCT ON (COALESCE(windows_answer_id,0),COALESCE(ubuntu_autoinstall_id,0),COALESCE(windows_profile_id,0),profile_title) `+answerCheckFields+` FROM answer_installation_checks ORDER BY COALESCE(windows_answer_id,0),COALESCE(ubuntu_autoinstall_id,0),COALESCE(windows_profile_id,0),profile_title,id DESC) latest ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	return scanAnswerChecks(rows)
}

// One extra row lets the HTTP layer expose a stable ID cursor for older history.
func (s Store) AnswerCheckHistory(ctx context.Context, kind string, answerID, before int64) ([]AnswerCheck, error) {
	column, err := checkFileColumn(kind)
	if err != nil || answerID <= 0 || before < 0 {
		return nil, fmt.Errorf("invalid history request")
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+answerCheckFields+` FROM answer_installation_checks WHERE `+column+`=$1 AND ($2::bigint=0 OR id<$2) ORDER BY id DESC LIMIT 21`, answerID, before)
	if err != nil {
		return nil, err
	}
	return scanAnswerChecks(rows)
}
