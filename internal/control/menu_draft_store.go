package control

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// MenuDraft returns the stored draft; ok is false when none was saved yet.
func (s Store) MenuDraft(ctx context.Context) (MenuDraft, bool, error) {
	var d MenuDraft
	err := s.Pool.QueryRow(ctx, `SELECT title,label,message,ubuntu,ubuntu_folder FROM menu_draft WHERE id=1`).Scan(&d.Title, &d.Label, &d.Message, &d.Ubuntu, &d.UbuntuFolder)
	if err == pgx.ErrNoRows {
		return MenuDraft{}, false, nil
	}
	if err != nil {
		return MenuDraft{}, false, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT key,folder,title FROM menu_draft_items ORDER BY position`)
	if err != nil {
		return MenuDraft{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var item MenuDraftItem
		if err := rows.Scan(&item.Key, &item.Folder, &item.Title); err != nil {
			return MenuDraft{}, false, err
		}
		d.Items = append(d.Items, item)
	}
	return d, true, rows.Err()
}

// SaveMenuDraft replaces the stored draft in one transaction.
func (s Store) SaveMenuDraft(ctx context.Context, d MenuDraft) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO menu_draft (id,title,label,message,ubuntu,ubuntu_folder,updated_at) VALUES (1,$1,$2,$3,$4,$5,now())
		ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title,label=EXCLUDED.label,message=EXCLUDED.message,ubuntu=EXCLUDED.ubuntu,ubuntu_folder=EXCLUDED.ubuntu_folder,updated_at=now()`,
		d.Title, d.Label, d.Message, d.Ubuntu, d.UbuntuFolder); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM menu_draft_items`); err != nil {
		return err
	}
	for i, item := range d.Items {
		if _, err := tx.Exec(ctx, `INSERT INTO menu_draft_items (position,key,folder,title) VALUES ($1,$2,$3,$4)`, i, item.Key, item.Folder, item.Title); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
