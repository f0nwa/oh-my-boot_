package winpe

import (
	"bytes"
	"fmt"
	"strconv"
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

// RenderProfileStartup binds a WinPE script to one prepared ISO profile.
func RenderProfileStartup(template []byte, password string, profileID int64, installFile string) ([]byte, error) {
	if profileID <= 0 || (installFile != "install.wim" && installFile != "install.esd") {
		return nil, fmt.Errorf("invalid Windows ISO profile")
	}
	const idToken = "__PXE_PROFILE_ID__"
	const fileToken = "__PXE_INSTALL_FILE__"
	if !bytes.Contains(template, []byte(idToken)) || !bytes.Contains(template, []byte(fileToken)) {
		return nil, fmt.Errorf("WinPE profile template is incomplete")
	}
	out, err := RenderStartup(template, password)
	if err != nil {
		return nil, err
	}
	out = bytes.ReplaceAll(out, []byte(idToken), []byte(strconv.FormatInt(profileID, 10)))
	out = bytes.ReplaceAll(out, []byte(fileToken), []byte(installFile))
	return out, nil
}
