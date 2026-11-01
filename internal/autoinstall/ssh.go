package autoinstall

import (
	"bytes"
	"errors"
	"golang.org/x/crypto/ssh"
)

type SSHValues struct {
	InstallServer  bool     `yaml:"install-server"`
	AllowPassword  bool     `yaml:"allow-pw"`
	AuthorizedKeys []string `yaml:"authorized-keys,omitempty"`
}

func normalizeSSH(s SSHValues) SSHValues {
	if !s.InstallServer {
		return SSHValues{}
	}
	s.AuthorizedKeys = uniqueStrings(s.AuthorizedKeys)
	return s
}
func validateSSH(s SSHValues) error {
	if !s.InstallServer {
		if s.AllowPassword || len(s.AuthorizedKeys) > 0 {
			return errors.New("ssh: disabled server has active settings")
		}
		return nil
	}
	if len(s.AuthorizedKeys) > 16 {
		return errors.New("ssh keys: at most 16 keys")
	}
	for _, line := range s.AuthorizedKeys {
		if !validText(line) {
			return errors.New("ssh keys: one public key per line required")
		}
		_, _, options, rest, e := ssh.ParseAuthorizedKey([]byte(line))
		if e != nil || len(options) > 0 || len(bytes.TrimSpace(rest)) > 0 {
			return errors.New("ssh keys: invalid public key or unauthorized options")
		}
	}
	return nil
}
