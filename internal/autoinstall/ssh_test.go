package autoinstall

import (
	"crypto/ed25519"
	"crypto/rand"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
)

func TestSSHKeys(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewPublicKey(pub)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	f := validForm()
	f.SSH = SSHValues{InstallServer: true, AuthorizedKeys: []string{line, line}}
	v, _, err := Compile(f)
	if err != nil || len(v.SSH.AuthorizedKeys) != 1 {
		t.Fatalf("key normalization %v", err)
	}
	for _, bad := range []string{"invalid", "command=\"id\" " + line, line + "\n" + line} {
		f.SSH.AuthorizedKeys = []string{bad}
		if _, _, err := Compile(f); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
