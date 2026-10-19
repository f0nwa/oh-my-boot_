package control

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"pxe-platform/internal/agent"
)

func (s Store) CreateDriverConfig(ctx context.Context, config DriverConfig) (DriverConfig, error) {
	selection, err := json.Marshal(config.PackageINFs)
	if err != nil {
		return config, err
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO winpe_driver_configs (profile_id,answer_id,title,folder,selected_infs) VALUES ($1,NULLIF($2,0),$3,$4,$5) RETURNING id,state`, config.ProfileID, config.AnswerID, config.Title, config.Folder, selection).Scan(&config.ID, &config.State)
	return config, err
}

func (s Store) DriverConfigs(ctx context.Context) ([]DriverConfig, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,profile_id,COALESCE(answer_id,0),title,folder,selected_infs,state,wim_key,wim_sha256,script_sha256,last_error FROM winpe_driver_configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DriverConfig{}
	for rows.Next() {
		var item DriverConfig
		var selection []byte
		if err := rows.Scan(&item.ID, &item.ProfileID, &item.AnswerID, &item.Title, &item.Folder, &selection, &item.State, &item.Build.WIMKey, &item.Build.WIMSHA256, &item.Build.ScriptSHA256, &item.Error); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(selection, &item.PackageINFs); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s Store) DriverConfig(ctx context.Context, id int64) (DriverConfig, error) {
	var item DriverConfig
	var selection []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,profile_id,COALESCE(answer_id,0),title,folder,selected_infs,state,wim_key,wim_sha256,script_sha256,last_error FROM winpe_driver_configs WHERE id=$1`, id).Scan(&item.ID, &item.ProfileID, &item.AnswerID, &item.Title, &item.Folder, &selection, &item.State, &item.Build.WIMKey, &item.Build.WIMSHA256, &item.Build.ScriptSHA256, &item.Error)
	if err != nil {
		return item, err
	}
	err = json.Unmarshal(selection, &item.PackageINFs)
	return item, err
}

func (s Store) SetDriverConfigResult(ctx context.Context, id int64, state string, build agent.DriverBuildResult, message string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE winpe_driver_configs SET state=$2,wim_key=$3,wim_sha256=$4,script_sha256=$5,last_error=$6 WHERE id=$1`, id, state, build.WIMKey, build.WIMSHA256, build.ScriptSHA256, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
