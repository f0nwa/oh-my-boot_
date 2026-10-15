package boot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxMenuSize = 256 << 10

// Publisher owns the durable menu snapshot and its live in-memory copy.
type Publisher struct {
	mu   sync.RWMutex
	path string
	menu []byte
	hash string
}

func NewPublisher(path string, initial []byte) (*Publisher, error) {
	if err := validateMenu(initial); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("snapshot path is required")
	}
	sum := sha256.Sum256(initial)
	return &Publisher{path: path, menu: append([]byte(nil), initial...), hash: hex.EncodeToString(sum[:])}, nil
}

func (p *Publisher) Current() ([]byte, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]byte(nil), p.menu...), p.hash
}

func (p *Publisher) Publish(menu []byte) (string, error) {
	if err := validateMenu(menu); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	dir := filepath.Dir(p.path)
	file, err := os.CreateTemp(dir, ".menu-*")
	if err != nil {
		return "", fmt.Errorf("create snapshot: %w", err)
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.Write(menu); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), p.path); err != nil {
		return "", fmt.Errorf("replace snapshot: %w", err)
	}
	sum := sha256.Sum256(menu)
	p.menu = append([]byte(nil), menu...)
	p.hash = hex.EncodeToString(sum[:])
	return p.hash, nil
}

func validateMenu(menu []byte) error {
	if len(menu) == 0 || len(menu) > maxMenuSize || !strings.HasPrefix(string(menu), "#!ipxe\n") {
		return fmt.Errorf("invalid iPXE menu snapshot")
	}
	return nil
}
