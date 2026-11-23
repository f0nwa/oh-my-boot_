package totp

import (
	"strings"
	"testing"
	"time"
)

func TestRFC6238SHA1Vectors(t *testing.T) {
	// RFC 6238 Appendix B test secret for SHA1: "12345678901234567890"
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string // last 6 digits of RFC 6238 8-digit vectors
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
	}
	for _, tc := range cases {
		got := GenerateCode(secret, time.Unix(tc.unix, 0))
		if got != tc.want {
			t.Fatalf("GenerateCode(%d) = %q, want %q", tc.unix, got, tc.want)
		}
	}
}

func TestVerifyCodeSkewAndReplayProtection(t *testing.T) {
	secret, b32Secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != SecretBytes || len(b32Secret) != 32 {
		t.Fatalf("unexpected secret lengths: raw=%d b32=%d", len(secret), len(b32Secret))
	}
	now := time.Unix(1700000010, 0)
	prevCode := GenerateCode(secret, now.Add(-30*time.Second))
	curCode := GenerateCode(secret, now)
	nextCode := GenerateCode(secret, now.Add(30*time.Second))
	oldCode := GenerateCode(secret, now.Add(-60*time.Second))

	step, ok := VerifyCode(secret, prevCode, now, 0)
	if !ok || step != Step(now)-1 {
		t.Fatalf("prevCode should pass skew -1: ok=%v step=%d", ok, step)
	}
	// Replay of the same step must be rejected.
	if _, ok := VerifyCode(secret, prevCode, now, step); ok {
		t.Fatal("replayed step was accepted")
	}
	step2, ok := VerifyCode(secret, curCode[:3]+" "+curCode[3:], now, step)
	if !ok || step2 != Step(now) {
		t.Fatalf("curCode with space should pass: ok=%v step=%d", ok, step2)
	}
	step3, ok := VerifyCode(secret, nextCode, now, step2)
	if !ok || step3 != Step(now)+1 {
		t.Fatalf("nextCode should pass skew +1: ok=%v step=%d", ok, step3)
	}
	if _, ok := VerifyCode(secret, oldCode, now, 0); ok {
		t.Fatal("code outside ±1 step window was accepted")
	}
	if _, ok := VerifyCode(secret, "abcdef", now, 0); ok {
		t.Fatal("non-numeric code was accepted")
	}
}

func TestRecoveryCodesAndKeyURIAndQRSVG(t *testing.T) {
	plain, hashes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("got %d plain, %d hashes", len(plain), len(hashes))
	}
	for i, code := range plain {
		if len(code) != 9 || code[4] != '-' {
			t.Fatalf("unexpected recovery code format: %q", code)
		}
		if HashRecoveryCode(strings.ToLower(strings.ReplaceAll(code, "-", " "))) != hashes[i] {
			t.Fatalf("normalized hash mismatch for %q", code)
		}
	}
	_, b32Secret, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	uri := KeyURI(DefaultIssuer, "admin", b32Secret)
	if !strings.HasPrefix(uri, "otpauth://totp/oh%20my%20boot_:admin?") || !strings.Contains(uri, "secret="+b32Secret) {
		t.Fatalf("unexpected KeyURI: %s", uri)
	}
	svg, err := RenderQRSVG(uri)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") || !strings.Contains(svg, "<path ") {
		t.Fatalf("invalid QR SVG: %s", svg[:min(len(svg), 120)])
	}
	matrix, err := EncodeQR([]byte(uri))
	if err != nil {
		t.Fatal(err)
	}
	// Finder pattern at top-left (7x7 outer ring dark, 5x5 inner ring light, 3x3 center dark).
	if !matrix[0][0] || matrix[1][1] || !matrix[3][3] {
		t.Fatalf("top-left finder pattern corrupted")
	}
}
