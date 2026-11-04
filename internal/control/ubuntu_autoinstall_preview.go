package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/openwall/yescrypt-go"
	"omb/internal/autoinstall"
	"reflect"
	"strings"
	"time"
)

const autoinstallPreviewLifetime = 10 * time.Minute
const maxAutoinstallPreviewToken = 128 << 10

type autoinstallPreviewPayload struct {
	Title   string             `json:"title"`
	Values  autoinstall.Values `json:"values"`
	Digest  string             `json:"digest"`
	Expires int64              `json:"expires"`
	ISOID   int64              `json:"iso_id,omitempty"`
	ISOSHA  string             `json:"iso_sha,omitempty"`
}

func signAutoinstallPreview(payload autoinstallPreviewPayload, session string) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(session))
	_, _ = mac.Write(data)
	token := base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(token) > maxAutoinstallPreviewToken {
		return "", fmt.Errorf("preview exceeds size limit")
	}
	return token, nil
}

func verifyAutoinstallPreview(token, session string) (autoinstallPreviewPayload, bool) {
	var payload autoinstallPreviewPayload
	if len(token) > maxAutoinstallPreviewToken {
		return payload, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return payload, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return payload, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return payload, false
	}
	mac := hmac.New(sha256.New, []byte(session))
	_, _ = mac.Write(data)
	if !hmac.Equal(signature, mac.Sum(nil)) || json.Unmarshal(data, &payload) != nil {
		return payload, false
	}
	now := time.Now().Unix()
	if payload.Expires <= now || payload.Expires > now+int64(autoinstallPreviewLifetime/time.Second) || !safeMenuText(payload.Title, 80) {
		return payload, false
	}
	rendered, err := autoinstall.Render(payload.Values)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(rendered)) != payload.Digest {
		return payload, false
	}
	return payload, true
}

func matchesAutoinstallPreview(payload autoinstallPreviewPayload, title string, form autoinstall.Form) bool {
	values, err := autoinstall.Normalize(form)
	if err != nil {
		return false
	}
	values.PasswordHash = payload.Values.PasswordHash
	if strings.TrimSpace(title) != payload.Title || !reflect.DeepEqual(values, payload.Values) {
		return false
	}
	if form.Password == "" {
		return true
	}
	if len(form.Password) > 1024 {
		return false
	}
	hash, err := yescrypt.Hash([]byte(form.Password), []byte(payload.Values.PasswordHash))
	return err == nil && hmac.Equal(hash, []byte(payload.Values.PasswordHash))
}
