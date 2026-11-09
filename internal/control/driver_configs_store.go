package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"omb/internal/agent"
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

func (s Store) DeleteDriverConfig(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM winpe_driver_configs WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SyncWinPEDriverScripts updates driver configuration startup checksums and all
// saved menu versions in one transaction after Agent rewrites WinPE wait loops.
func (s Store) SyncWinPEDriverScripts(ctx context.Context, updates []agent.DriverScriptUpdate) (string, error) {
	if len(updates) == 0 {
		return "", nil
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `LOCK TABLE menu_versions IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return "", err
	}
	for _, u := range updates {
		if u.OldSHA == u.NewSHA {
			if _, err := tx.Exec(ctx, `UPDATE winpe_driver_configs SET script_sha256=$1 WHERE id=$2 AND state='ready' AND script_sha256 <> $1`, u.NewSHA, u.ConfigID); err != nil {
				return "", err
			}
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE winpe_driver_configs SET script_sha256=$1 WHERE id=$2 AND script_sha256 IN ($3,$1)`, u.NewSHA, u.ConfigID, u.OldSHA); err != nil {
			return "", err
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,script,active FROM menu_versions WHERE position($1 in script)>0 ORDER BY id FOR UPDATE`, menuDriverMarker)
	if err != nil {
		return "", err
	}
	type menuUpdate struct {
		id     int64
		script string
		sha256 string
		active bool
	}
	var pending []menuUpdate
	var activeScript string
	for rows.Next() {
		var id int64
		var script string
		var active bool
		if err := rows.Scan(&id, &script, &active); err != nil {
			rows.Close()
			return "", err
		}
		updated, changed, err := updateMenuDriverScriptHashes(script, updates)
		if err != nil {
			rows.Close()
			return "", fmt.Errorf("menu version %d: %w", id, err)
		}
		if active {
			activeScript = updated
		}
		if changed {
			sum := sha256.Sum256([]byte(updated))
			pending = append(pending, menuUpdate{
				id:     id,
				script: updated,
				sha256: hex.EncodeToString(sum[:]),
				active: active,
			})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()

	for _, m := range pending {
		if _, err := tx.Exec(ctx, `UPDATE menu_versions SET script=$1,sha256=$2 WHERE id=$3`, m.script, m.sha256, m.id); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return activeScript, nil
}
