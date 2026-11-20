package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	secretBoxVersion byte = 1
	secretKeySize         = 32
	secretNonceSize       = 12
)

var (
	ErrSecretsKeyMissing = errors.New("secrets key file is missing")
	ErrSecretsDecrypt    = errors.New("cannot decrypt stored integration secrets")
)

// SecretBox encrypts and decrypts integration secrets using AES-256-GCM.
type SecretBox struct {
	aead cipher.AEAD
}

// NewSecretBox constructs a SecretBox from a raw 32-byte key.
func NewSecretBox(key []byte) (*SecretBox, error) {
	if len(key) != secretKeySize {
		return nil, fmt.Errorf("secrets key must be %d bytes, got %d", secretKeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SecretBox{aead: aead}, nil
}

// LoadSecretBox reads a 32-byte key (raw binary or 64 hex chars) from path.
func LoadSecretBox(path string) (*SecretBox, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSecretsKeyMissing
		}
		return nil, fmt.Errorf("read secrets key %s: %w", path, err)
	}
	key, err := parseSecretKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid secrets key %s: %w", path, err)
	}
	return NewSecretBox(key)
}

func parseSecretKey(raw []byte) ([]byte, error) {
	if len(raw) == secretKeySize {
		out := make([]byte, secretKeySize)
		copy(out, raw)
		return out, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == secretKeySize*2 {
		decoded, err := hex.DecodeString(trimmed)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("expected %d raw bytes or %d hex chars", secretKeySize, secretKeySize*2)
}

// GenerateSecretKeyFile creates a new random 32-byte hex key file with mode 0600.
func GenerateSecretKeyFile(path string) (*SecretBox, error) {
	key := make([]byte, secretKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	box, err := NewSecretBox(key)
	if err != nil {
		return nil, err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	content := []byte(hex.EncodeToString(key) + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, err
	}
	return box, nil
}

// Encrypt seals plaintext bound to the provider AAD.
func (b *SecretBox) Encrypt(provider string, plaintext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, ErrSecretsKeyMissing
	}
	nonce := make([]byte, secretNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+secretNonceSize+len(plaintext)+b.aead.Overhead())
	out = append(out, secretBoxVersion)
	out = append(out, nonce...)
	out = b.aead.Seal(out, nonce, plaintext, []byte("omb-auth-integration:"+provider))
	return out, nil
}

// Decrypt opens ciphertext bound to the provider AAD.
func (b *SecretBox) Decrypt(provider string, ciphertext []byte) ([]byte, error) {
	if b == nil || b.aead == nil {
		return nil, ErrSecretsKeyMissing
	}
	if len(ciphertext) < 1+secretNonceSize+b.aead.Overhead() || ciphertext[0] != secretBoxVersion {
		return nil, ErrSecretsDecrypt
	}
	nonce := ciphertext[1 : 1+secretNonceSize]
	sealed := ciphertext[1+secretNonceSize:]
	plain, err := b.aead.Open(nil, nonce, sealed, []byte("omb-auth-integration:"+provider))
	if err != nil {
		return nil, ErrSecretsDecrypt
	}
	return plain, nil
}
