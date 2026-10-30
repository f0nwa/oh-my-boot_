package autoinstall

import (
	"crypto/rand"
	"errors"
	"regexp"

	"github.com/openwall/yescrypt-go"
)

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// This constructor only accepts its own bounded yescrypt settings. In particular,
// stored values cannot request a more expensive hash computation in the editor.
var passwordHashPattern = regexp.MustCompile(`^\$y\$j9T\$[./0-9A-Za-z]{21}[./01]\$[./0-9A-Za-z]{42}[./0-9A-D]$`)

func hashPassword(password string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("password: could not generate salt")
	}
	// crypt's base64 uses little-endian groups and no padding.
	salt := make([]byte, 0, 22)
	var bits uint32
	var count uint
	for _, b := range random {
		bits |= uint32(b) << count
		count += 8
		for count >= 6 {
			salt = append(salt, cryptAlphabet[bits&63])
			bits >>= 6
			count -= 6
		}
	}
	if count > 0 {
		salt = append(salt, cryptAlphabet[bits&63])
	}
	hash, err := yescrypt.Hash([]byte(password), []byte("$y$j9T$"+string(salt)))
	if err != nil {
		return "", errors.New("password: could not generate hash")
	}
	return string(hash), nil
}
