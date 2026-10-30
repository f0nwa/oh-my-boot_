package control

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// EnsureBootProfiles creates the one system profile. Library imports belong to
// the one-time stand migration; startup never regenerates installations.
func (s Store) EnsureBootProfiles(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO boot_profiles(kind,title) VALUES('ubuntu_builtin','Ubuntu Desktop') ON CONFLICT (kind) WHERE kind='ubuntu_builtin' DO NOTHING`)
	return err
}

func (s Store) BootProfiles(ctx context.Context) ([]BootProfile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT b.id,b.kind,b.title,COALESCE(w.windows_profile_id,0) FROM boot_profiles b LEFT JOIN windows_boot_profiles w ON w.boot_profile_id=b.id ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BootProfile{}
	for rows.Next() {
		var profile BootProfile
		if err := rows.Scan(&profile.ID, &profile.Kind, &profile.Title, &profile.WindowsProfileID); err != nil {
			return nil, err
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s Store) Installations(ctx context.Context) ([]Installation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT i.id,i.boot_profile_id,i.title,i.folder,COALESCE(w.answer_id,0),COALESCE(w.driver_config_id,0),COALESCE(i.autoinstall_id,0) FROM installations i LEFT JOIN windows_installation_options w ON w.installation_id=i.id ORDER BY i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Installation{}
	for rows.Next() {
		var item Installation
		if err := rows.Scan(&item.ID, &item.BootProfileID, &item.Title, &item.Folder, &item.AnswerID, &item.DriverConfigID, &item.AutoinstallID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s Store) CreateInstallation(ctx context.Context, item Installation) (Installation, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Installation{}, err
	}
	defer tx.Rollback(ctx)
	var profile BootProfile
	err = tx.QueryRow(ctx, `SELECT b.id,b.kind,b.title,COALESCE(w.windows_profile_id,0) FROM boot_profiles b LEFT JOIN windows_boot_profiles w ON w.boot_profile_id=b.id WHERE b.id=$1 FOR SHARE OF b`, item.BootProfileID).Scan(&profile.ID, &profile.Kind, &profile.Title, &profile.WindowsProfileID)
	if err != nil {
		return Installation{}, err
	}
	// Lock selected components while validating and writing their references.
	var configs []DriverConfig
	if item.DriverConfigID > 0 {
		var config DriverConfig
		err := tx.QueryRow(ctx, `SELECT id,profile_id,COALESCE(answer_id,0) FROM winpe_driver_configs WHERE id=$1 FOR SHARE`, item.DriverConfigID).Scan(&config.ID, &config.ProfileID, &config.AnswerID)
		if err != nil {
			return Installation{}, err
		}
		configs = append(configs, config)
	}
	var answers []WindowsAnswerVariant
	answerID := item.AnswerID
	if len(configs) > 0 {
		answerID = configs[0].AnswerID
	}
	if answerID > 0 {
		var answer WindowsAnswerVariant
		if err := tx.QueryRow(ctx, `SELECT id,profile_id FROM windows_answer_variants WHERE id=$1 FOR SHARE`, answerID).Scan(&answer.ID, &answer.ProfileID); err != nil {
			return Installation{}, err
		}
		answers = append(answers, answer)
	}
	var files []UbuntuAutoinstallSummary
	if item.AutoinstallID > 0 {
		var file UbuntuAutoinstallSummary
		if err := tx.QueryRow(ctx, `SELECT id,state FROM ubuntu_autoinstall_files WHERE id=$1 FOR SHARE`, item.AutoinstallID).Scan(&file.ID, &file.State); err != nil {
			return Installation{}, err
		}
		files = append(files, file)
	}
	if err := ValidateInstallation(item, profile, answers, configs, files); err != nil {
		return Installation{}, err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO installations(boot_profile_id,title,folder,autoinstall_id) VALUES($1,$2,$3,NULLIF($4,0)) RETURNING id`, item.BootProfileID, item.Title, item.Folder, item.AutoinstallID).Scan(&item.ID); err != nil {
		return Installation{}, err
	}
	if profile.Kind == BootProfileWindowsISO {
		if _, err := tx.Exec(ctx, `INSERT INTO windows_installation_options(installation_id,answer_id,driver_config_id) VALUES($1,NULLIF($2,0),NULLIF($3,0))`, item.ID, item.AnswerID, item.DriverConfigID); err != nil {
			return Installation{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Installation{}, err
	}
	return item, nil
}

func createWindowsBootProfile(ctx context.Context, tx pgx.Tx, profile WindowsProfile) error {
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO boot_profiles(kind,title) VALUES('windows_iso',$1) RETURNING id`, profile.Title).Scan(&id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO windows_boot_profiles(boot_profile_id,windows_profile_id) VALUES($1,$2)`, id, profile.ID)
	return err
}
