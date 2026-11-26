package control

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"omb/internal/agent"

	"github.com/jackc/pgx/v5"
)

func (s Store) BootProfiles(ctx context.Context) ([]BootProfile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT b.id,b.kind,b.title,COALESCE(w.windows_profile_id,0),COALESCE(wi.install_image_id,0),COALESCE(u.id,0),COALESCE(u.storage_id,0),COALESCE(u.path,''),COALESCE(u.sha256,''),COALESCE(u.state,'') FROM boot_profiles b LEFT JOIN windows_boot_profiles w ON w.boot_profile_id=b.id LEFT JOIN windows_install_boot_profiles wi ON wi.boot_profile_id=b.id LEFT JOIN ubuntu_sources u ON u.boot_profile_id=b.id ORDER BY b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BootProfile{}
	for rows.Next() {
		var profile BootProfile
		if err := rows.Scan(&profile.ID, &profile.Kind, &profile.Title, &profile.WindowsProfileID, &profile.InstallImageID, &profile.UbuntuSourceID, &profile.UbuntuStorageID, &profile.UbuntuPath, &profile.UbuntuSHA256, &profile.UbuntuState); err != nil {
			return nil, err
		}
		result = append(result, profile)
	}
	return result, rows.Err()
}

func (s Store) Installations(ctx context.Context) ([]Installation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT i.id,i.boot_profile_id,i.title,i.folder,COALESCE(w.answer_id,0),COALESCE(w.driver_config_id,0),COALESCE(w.boot_image_id,0),COALESCE(w.install_image_id,0),COALESCE(i.autoinstall_id,0) FROM installations i LEFT JOIN windows_installation_options w ON w.installation_id=i.id ORDER BY i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Installation{}
	for rows.Next() {
		var item Installation
		if err := rows.Scan(&item.ID, &item.BootProfileID, &item.Title, &item.Folder, &item.AnswerID, &item.DriverConfigID, &item.BootImageID, &item.InstallImageID, &item.AutoinstallID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s Store) CreateInstallation(ctx context.Context, item Installation) (Installation, error) {
	return s.saveInstallation(ctx, item, false)
}
func (s Store) UpdateInstallation(ctx context.Context, item Installation) error {
	_, err := s.saveInstallation(ctx, item, true)
	return err
}

var errInstallationPublished = errors.New("boot entry is published")

// DeleteInstallation removes the entry and its draft placement in one
// transaction. Library components and historical menu snapshots stay intact.
func (s Store) DeleteInstallation(ctx context.Context, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Serialize with SaveActiveMenu before locking the entry, so a concurrent
	// publication cannot make it active between the check and the deletion.
	if _, err := tx.Exec(ctx, `LOCK TABLE menu_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	var existing int64
	if err := tx.QueryRow(ctx, `SELECT id FROM installations WHERE id=$1 FOR UPDATE`, id).Scan(&existing); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM menu_version_items i JOIN menu_versions v ON v.id=i.version_id WHERE v.active AND i.installation_id=$1)`, id).Scan(&active); err != nil {
		return err
	}
	if active {
		return errInstallationPublished
	}
	if _, err := tx.Exec(ctx, `UPDATE menu_draft SET default_installation_id=0 WHERE default_installation_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM menu_draft_items WHERE installation_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM installations WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s Store) saveInstallation(ctx context.Context, item Installation, update bool) (Installation, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Installation{}, err
	}
	defer tx.Rollback(ctx)
	if update {
		var sourceID int64
		if err := tx.QueryRow(ctx, `SELECT boot_profile_id FROM installations WHERE id=$1 FOR UPDATE`, item.ID).Scan(&sourceID); err != nil {
			return Installation{}, err
		}
		if sourceID != item.BootProfileID {
			return Installation{}, fmt.Errorf("download source is immutable")
		}
	}
	var profile BootProfile
	err = tx.QueryRow(ctx, `SELECT b.id,b.kind,b.title,COALESCE(w.windows_profile_id,0),COALESCE(wi.install_image_id,0),COALESCE(u.id,0),COALESCE(u.storage_id,0),COALESCE(u.path,''),COALESCE(u.sha256,''),COALESCE(u.state,'') FROM boot_profiles b LEFT JOIN windows_boot_profiles w ON w.boot_profile_id=b.id LEFT JOIN windows_install_boot_profiles wi ON wi.boot_profile_id=b.id LEFT JOIN ubuntu_sources u ON u.boot_profile_id=b.id WHERE b.id=$1 FOR SHARE OF b`, item.BootProfileID).Scan(&profile.ID, &profile.Kind, &profile.Title, &profile.WindowsProfileID, &profile.InstallImageID, &profile.UbuntuSourceID, &profile.UbuntuStorageID, &profile.UbuntuPath, &profile.UbuntuSHA256, &profile.UbuntuState)
	if err != nil {
		return Installation{}, err
	}
	if profile.UbuntuSourceID > 0 {
		if err := tx.QueryRow(ctx, `SELECT state FROM ubuntu_sources WHERE id=$1 FOR SHARE`, profile.UbuntuSourceID).Scan(&profile.UbuntuState); err != nil {
			return Installation{}, err
		}
	}
	if !update && profile.Kind == BootProfileUbuntuISO && profile.UbuntuState != "verified" && profile.UbuntuState != "ready" && profile.UbuntuState != "error" {
		return Installation{}, fmt.Errorf("Ubuntu source has not been verified")
	}
	var winProfile WindowsProfile
	if isISOBootKind(profile.Kind) && profile.WindowsProfileID > 0 {
		if err := tx.QueryRow(ctx, `SELECT id,wim_info FROM windows_profiles WHERE id=$1 FOR SHARE`, profile.WindowsProfileID).Scan(&winProfile.ID, &winProfile.WIMInfo); err != nil {
			return Installation{}, err
		}
	}
	// Lock selected Windows boot.wim / install.wim images while validating.
	var bootImgHasSetup bool
	if item.BootImageID > 0 {
		if err := tx.QueryRow(ctx, `SELECT has_setup_exe FROM windows_boot_images WHERE id=$1 FOR SHARE`, item.BootImageID).Scan(&bootImgHasSetup); err != nil {
			return Installation{}, err
		}
	}
	var installImgWIM agent.WindowsWIMInfo
	if item.InstallImageID > 0 {
		if err := tx.QueryRow(ctx, `SELECT wim_info FROM windows_install_images WHERE id=$1 FOR SHARE`, item.InstallImageID).Scan(&installImgWIM); err != nil {
			return Installation{}, err
		}
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
		if err := tx.QueryRow(ctx, `SELECT id,state,target_summary FROM windows_answer_variants WHERE id=$1 FOR SHARE`, answerID).Scan(&answer.ID, &answer.State, &answer.Target); err != nil {
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
	if profile.Kind == BootProfileWindowsWIM && !bootImgHasSetup {
		return Installation{}, fmt.Errorf("selected boot.wim does not contain setup.exe")
	}
	activeWIM := winProfile.WIMInfo
	if item.InstallImageID > 0 && installImgWIM.Known {
		activeWIM = installImgWIM
	}
	if len(answers) > 0 && activeWIM.Known {
		if res := EvaluateWindowsAnswerCompatibility(answers[0].Target, activeWIM); res.Status == "incompatible" {
			return Installation{}, fmt.Errorf("%s", strings.Join(res.Errors, "; "))
		}
	}
	if update {
		if _, err := tx.Exec(ctx, `UPDATE installations SET title=$2,folder=$3,autoinstall_id=NULLIF($4,0) WHERE id=$1`, item.ID, item.Title, item.Folder, item.AutoinstallID); err != nil {
			return Installation{}, err
		}
	} else {
		if err := tx.QueryRow(ctx, `INSERT INTO installations(boot_profile_id,title,folder,autoinstall_id) VALUES($1,$2,$3,NULLIF($4,0)) RETURNING id`, item.BootProfileID, item.Title, item.Folder, item.AutoinstallID).Scan(&item.ID); err != nil {
			return Installation{}, err
		}
	}
	if isWindowsBootKind(profile.Kind) {
		if _, err := tx.Exec(ctx, `INSERT INTO windows_installation_options(installation_id,answer_id,driver_config_id,boot_image_id,install_image_id) VALUES($1,NULLIF($2,0),NULLIF($3,0),NULLIF($4,0),NULLIF($5,0)) ON CONFLICT(installation_id) DO UPDATE SET answer_id=EXCLUDED.answer_id,driver_config_id=EXCLUDED.driver_config_id,boot_image_id=EXCLUDED.boot_image_id,install_image_id=EXCLUDED.install_image_id`, item.ID, item.AnswerID, item.DriverConfigID, item.BootImageID, item.InstallImageID); err != nil {
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
	kind := BootProfileWindowsISO
	if profile.BootMode == agent.ISOStrelecMode {
		kind = BootProfileWinPEISO
	}
	if err := tx.QueryRow(ctx, `INSERT INTO boot_profiles(kind,title) VALUES($1,$2) RETURNING id`, kind, profile.Title).Scan(&id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO windows_boot_profiles(boot_profile_id,windows_profile_id) VALUES($1,$2)`, id, profile.ID)
	return err
}

func createWindowsInstallBootProfile(ctx context.Context, tx pgx.Tx, img WindowsInstallImage) error {
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO boot_profiles(kind,title) VALUES($1,$2) RETURNING id`, BootProfileWindowsWIM, img.Title).Scan(&id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO windows_install_boot_profiles(boot_profile_id,install_image_id) VALUES($1,$2)`, id, img.ID)
	return err
}
