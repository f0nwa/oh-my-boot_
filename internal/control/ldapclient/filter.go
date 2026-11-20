package ldapclient

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// EscapeFilter escapes a value for safe interpolation into an RFC 4515 LDAP filter.
func EscapeFilter(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '*', '(', ')', 0:
			fmt.Fprintf(&b, "\\%02x", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// EscapeBytesFilter formats raw bytes (such as binary AD objectGUID) for an LDAP filter.
func EscapeBytesFilter(raw []byte) string {
	var b strings.Builder
	b.Grow(len(raw) * 3)
	for _, c := range raw {
		fmt.Fprintf(&b, "\\%02x", c)
	}
	return b.String()
}

// FormatADGUID converts a 16-byte Microsoft AD objectGUID into canonical UUID string format.
func FormatADGUID(raw []byte) (string, error) {
	if len(raw) != 16 {
		return "", fmt.Errorf("objectGUID must be 16 bytes, got %d", len(raw))
	}
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		raw[3], raw[2], raw[1], raw[0],
		raw[5], raw[4],
		raw[7], raw[6],
		raw[8], raw[9],
		raw[10], raw[11], raw[12], raw[13], raw[14], raw[15]), nil
}

// ParseADGUID parses a canonical UUID string back into a 16-byte Microsoft AD objectGUID.
func ParseADGUID(s string) ([]byte, error) {
	clean := strings.ReplaceAll(strings.TrimSpace(s), "-", "")
	if len(clean) != 32 {
		return nil, fmt.Errorf("invalid AD GUID %q", s)
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	out[0], out[1], out[2], out[3] = b[3], b[2], b[1], b[0]
	out[4], out[5] = b[5], b[4]
	out[6], out[7] = b[7], b[6]
	copy(out[8:], b[8:])
	return out, nil
}

// CompileFilter compiles an RFC 4515 LDAP search filter into BER bytes.
func CompileFilter(filter string) ([]byte, error) {
	filter = strings.TrimSpace(filter)
	if len(filter) < 2 || filter[0] != '(' || filter[len(filter)-1] != ')' {
		return nil, fmt.Errorf("invalid LDAP filter %q", filter)
	}
	out, pos, err := compileFilterAt(filter, 0)
	if err != nil {
		return nil, err
	}
	if pos != len(filter) {
		return nil, fmt.Errorf("trailing characters in LDAP filter %q", filter)
	}
	return out, nil
}

func compileFilterAt(s string, pos int) ([]byte, int, error) {
	if pos >= len(s) || s[pos] != '(' {
		return nil, pos, errors.New("expected '(' in LDAP filter")
	}
	pos++
	if pos >= len(s) {
		return nil, pos, errors.New("unexpected end of LDAP filter")
	}
	switch s[pos] {
	case '&', '|':
		tag := byte(0xA0) // AND
		if s[pos] == '|' {
			tag = 0xA1 // OR
		}
		pos++
		var children [][]byte
		for pos < len(s) && s[pos] != ')' {
			child, next, err := compileFilterAt(s, pos)
			if err != nil {
				return nil, next, err
			}
			children = append(children, child)
			pos = next
		}
		if pos >= len(s) || s[pos] != ')' || len(children) == 0 {
			return nil, pos, errors.New("malformed compound LDAP filter")
		}
		return encodeSequence(tag, children...), pos + 1, nil
	case '!':
		pos++
		child, next, err := compileFilterAt(s, pos)
		if err != nil {
			return nil, next, err
		}
		if next >= len(s) || s[next] != ')' {
			return nil, next, errors.New("malformed NOT LDAP filter")
		}
		return encodeTag(0xA2, child), next + 1, nil
	default:
		end := strings.IndexByte(s[pos:], ')')
		if end < 0 {
			return nil, pos, errors.New("unclosed '(' in LDAP filter")
		}
		expr := s[pos : pos+end]
		encoded, err := compileSimpleFilter(expr)
		if err != nil {
			return nil, pos, err
		}
		return encoded, pos + end + 1, nil
	}
}

func compileSimpleFilter(expr string) ([]byte, error) {
	// Extensible match: attr:rule:=value (used by AD BIT_AND and IN_CHAIN rules).
	if idx := strings.Index(expr, ":="); idx > 0 {
		left := expr[:idx]
		val, err := unescapeFilterValue(expr[idx+2:])
		if err != nil {
			return nil, err
		}
		parts := strings.Split(left, ":")
		attr := parts[0]
		var rule string
		var dnAttrs bool
		for _, p := range parts[1:] {
			if strings.EqualFold(p, "dn") {
				dnAttrs = true
			} else if p != "" {
				rule = p
			}
		}
		var items [][]byte
		if rule != "" {
			items = append(items, encodeTag(0x81, []byte(rule)))
		}
		if attr != "" {
			items = append(items, encodeTag(0x82, []byte(attr)))
		}
		items = append(items, encodeTag(0x83, val))
		if dnAttrs {
			items = append(items, encodeTag(0x84, []byte{0xff}))
		}
		return encodeSequence(0xA9, items...), nil
	}

	idx := strings.IndexByte(expr, '=')
	if idx <= 0 {
		return nil, fmt.Errorf("invalid filter expression %q", expr)
	}
	attr := expr[:idx]
	rawVal := expr[idx+1:]
	if rawVal == "*" {
		// Present filter: context-specific primitive 7 (0x87)
		return encodeTag(0x87, []byte(attr)), nil
	}
	val, err := unescapeFilterValue(rawVal)
	if err != nil {
		return nil, err
	}
	// EqualityMatch: context-specific constructed 3 (0xA3)
	return encodeSequence(0xA3, encodeString(attr), encodeTag(tagOctetString, val)), nil
}

func unescapeFilterValue(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+2 >= len(s) {
				return nil, fmt.Errorf("truncated escape in LDAP filter value %q", s)
			}
			b, err := hex.DecodeString(s[i+1 : i+3])
			if err != nil {
				return nil, fmt.Errorf("invalid hex escape in LDAP filter value %q", s)
			}
			out = append(out, b[0])
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out, nil
}
