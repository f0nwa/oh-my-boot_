package control

import (
	"context"
	"encoding/json"
	"time"

	"pxe-platform/internal/drivers"
)

type DriverPackage struct {
	SHA256    string
	Inventory drivers.Inventory
	CreatedAt time.Time
}

func (s Store) CreateDriverPackage(ctx context.Context, inventory drivers.Inventory) error {
	data, err := json.Marshal(inventory)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO winpe_driver_packages (sha256, inventory) VALUES ($1,$2) ON CONFLICT (sha256) DO NOTHING`, inventory.SHA256, data)
	return err
}

func (s Store) DriverPackages(ctx context.Context) ([]DriverPackage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT sha256, inventory, created_at FROM winpe_driver_packages ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DriverPackage{}
	for rows.Next() {
		var item DriverPackage
		var data []byte
		if err := rows.Scan(&item.SHA256, &data, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &item.Inventory); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
