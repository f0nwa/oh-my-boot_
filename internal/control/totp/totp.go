package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	PeriodSeconds      = 30
	Digits             = 6
	SecretBytes        = 20
	RecoveryCodeCount  = 8
	recoveryAlphabet   = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 32 unambiguous chars (5 bits each)
	DefaultIssuer      = "Oh My Boot"
	allowedSkewSteps   = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret returns 20 cryptographically random bytes and their Base32 representation.
func GenerateSecret() ([]byte, string, error) {
	raw := make([]byte, SecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("generate totp secret: %w", err)
	}
	return raw, b32.EncodeToString(raw), nil
}

// FormatSecret groups a Base32 secret into 4-character blocks for manual entry.
func FormatSecret(b32Secret string) string {
	clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(b32Secret), " ", ""))
	var parts []string
	for i := 0; i < len(clean); i += 4 {
		end := i + 4
		if end > len(clean) {
			end = len(clean)
		}
		parts = append(parts, clean[i:end])
	}
	return strings.Join(parts, " ")
}

// KeyURI constructs a standard otpauth://totp/... URI for authenticator apps.
func KeyURI(issuer, account, b32Secret string) string {
	if issuer == "" {
		issuer = DefaultIssuer
	}
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", strings.ToUpper(strings.TrimSpace(b32Secret)))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Step returns the RFC 6238 time-step counter for timestamp t.
func Step(t time.Time) int64 {
	return t.Unix() / PeriodSeconds
}

// GenerateCodeAtStep computes the 6-digit RFC 6238 / RFC 4226 HMAC-SHA1 code for a counter step.
func GenerateCodeAtStep(secret []byte, step int64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	binCode := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", binCode%1000000)
}

// GenerateCode computes the 6-digit TOTP code for timestamp t.
func GenerateCode(secret []byte, t time.Time) string {
	return GenerateCodeAtStep(secret, Step(t))
}

// NormalizeCode strips spaces and dashes from a user-entered OTP code.
func NormalizeCode(code string) string {
	code = strings.TrimSpace(code)
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ReplaceAll(code, "-", "")
	return code
}

// VerifyCode validates a 6-digit TOTP code within [-1, +1] time steps of now.
// To prevent replay, matchedStep must be strictly greater than lastStep.
func VerifyCode(secret []byte, code string, now time.Time, lastStep int64) (matchedStep int64, ok bool) {
	clean := NormalizeCode(code)
	if len(clean) != Digits || len(secret) == 0 {
		return 0, false
	}
	for i := 0; i < len(clean); i++ {
		if clean[i] < '0' || clean[i] > '9' {
			return 0, false
		}
	}
	cur := Step(now)
	for delta := int64(-allowedSkewSteps); delta <= allowedSkewSteps; delta++ {
		step := cur + delta
		if step <= lastStep {
			continue
		}
		expected := GenerateCodeAtStep(secret, step)
		if subtle.ConstantTimeCompare([]byte(clean), []byte(expected)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// GenerateRecoveryCodes generates n single-use recovery codes (XXXX-XXXX) and their SHA-256 hex hashes.
func GenerateRecoveryCodes(n int) (plain []string, hashes []string, err error) {
	if n <= 0 {
		n = RecoveryCodeCount
	}
	plain = make([]string, 0, n)
	hashes = make([]string, 0, n)
	seen := make(map[string]struct{}, n)
	buf := make([]byte, 8)
	for len(plain) < n {
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		var chars [8]byte
		for i := 0; i < 8; i++ {
			chars[i] = recoveryAlphabet[int(buf[i])%len(recoveryAlphabet)]
		}
		code := string(chars[:4]) + "-" + string(chars[4:])
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		plain = append(plain, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return plain, hashes, nil
}

// HashRecoveryCode normalizes a recovery code (uppercase, no dashes/spaces) and returns its SHA-256 hex digest.
func HashRecoveryCode(code string) string {
	clean := strings.ToUpper(NormalizeCode(code))
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:])
}
