package winpe

import (
	"bytes"
	"fmt"
)

const passwordToken = "__PXE_ISO_PASSWORD__"

// RenderStartup inserts a limited downstream SMB credential into a WinPE script.
func RenderStartup(template []byte, password string) ([]byte, error) {
	if len(password) < 32 || len(password) > 64 {
		return nil, fmt.Errorf("downstream password must have 32–64 alphanumeric characters")
	}
	for _, b := range []byte(password) {
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')) {
			return nil, fmt.Errorf("downstream password must be alphanumeric")
		}
	}
	if bytes.Count(template, []byte(passwordToken)) != 1 {
		return nil, fmt.Errorf("WinPE template must contain exactly one password token")
	}
	return bytes.Replace(template, []byte(passwordToken), []byte(password), 1), nil
}
