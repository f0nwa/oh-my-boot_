package agent

import (
	"context"
	"fmt"
	"strconv"
)

func (m *DriverManager) RemoveConfig(_ context.Context, id int64, wimKey string, removeWIM bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id <= 0 || (wimKey != "" && !validManagedChild(wimKey)) || (removeWIM && len(wimKey) != 64) {
		return fmt.Errorf("invalid driver configuration identity")
	}
	if err := removeManagedDirectory(m.ConfigRoot, strconv.FormatInt(id, 10)); err != nil {
		return err
	}
	if removeWIM {
		return removeManagedDirectory(m.WIMRoot, wimKey)
	}
	return nil
}
