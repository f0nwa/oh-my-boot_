package control

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSecretBoxRoundTripAndProviderBinding(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "secrets.key")

	box, err := GenerateSecretKeyFile(keyPath)
	if err != nil {
		t.Fatalf("GenerateSecretKeyFile: %v", err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o, want 0600", info.Mode().Perm())
	}

	loaded, err := LoadSecretBox(keyPath)
	if err != nil {
		t.Fatalf("LoadSecretBox: %v", err)
	}

	plain := []byte(`{"bind_password":"super-secret-password"}`)
	enc, err := box.Encrypt("ldap", plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(enc, []byte("super-secret-password")) {
		t.Fatal("ciphertext contains plaintext secret")
	}

	got, err := loaded.Decrypt("ldap", enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Decrypt = %q, want %q", got, plain)
	}

	// Wrong provider AAD must fail.
	if _, err := loaded.Decrypt("keycloak", enc); !errors.Is(err, ErrSecretsDecrypt) {
		t.Fatalf("wrong provider error = %v, want ErrSecretsDecrypt", err)
	}

	// Tampered ciphertext must fail.
	tampered := append([]byte(nil), enc...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := loaded.Decrypt("ldap", tampered); !errors.Is(err, ErrSecretsDecrypt) {
		t.Fatalf("tampered error = %v, want ErrSecretsDecrypt", err)
	}

	// Different key must fail.
	otherBox, err := GenerateSecretKeyFile(filepath.Join(dir, "other.key"))
	if err != nil {
		t.Fatalf("other key: %v", err)
	}
	if _, err := otherBox.Decrypt("ldap", enc); !errors.Is(err, ErrSecretsDecrypt) {
		t.Fatalf("wrong key error = %v, want ErrSecretsDecrypt", err)
	}
}

func TestLoadSecretBoxMissingFile(t *testing.T) {
	_, err := LoadSecretBox(filepath.Join(t.TempDir(), "missing.key"))
	if !errors.Is(err, ErrSecretsKeyMissing) {
		t.Fatalf("err = %v, want ErrSecretsKeyMissing", err)
	}
}
