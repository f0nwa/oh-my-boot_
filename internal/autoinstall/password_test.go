package autoinstall

import (
	"bytes"
	"strings"
	"testing"

	"github.com/openwall/yescrypt-go"
)

func TestCompilePasswordUsesRandomYescryptSalt(t *testing.T) {
	form := validForm()
	first, _, err := Compile(form)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := Compile(form)
	if err != nil {
		t.Fatal(err)
	}
	if first.PasswordHash == second.PasswordHash {
		t.Fatal("password salts reused")
	}
	for _, hash := range []string{first.PasswordHash, second.PasswordHash} {
		if !strings.HasPrefix(hash, "$y$j9T$") {
			t.Fatal("wrong password format")
		}
		verified, err := yescrypt.Hash([]byte(form.Password), []byte(hash))
		if err != nil || !bytes.Equal(verified, []byte(hash)) {
			t.Fatal("password hash does not verify")
		}
		wrong, err := yescrypt.Hash([]byte("wrong-password"), []byte(hash))
		if err != nil || bytes.Equal(wrong, []byte(hash)) {
			t.Fatal("incorrect password verified")
		}
	}
}
