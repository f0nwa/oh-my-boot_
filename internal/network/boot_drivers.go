package network

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// BootDriverRule selects a UEFI driver for an exact MAC or a 3–5 byte prefix.
// Prefixes may be bare or padded to six bytes with trailing wildcard bytes.
type BootDriverRule struct {
	Match  string `json:"match"`
	Driver string `json:"driver"`
}

func canonicalDriverMatch(match string) (string, string, error) {
	parts := strings.Split(strings.ToLower(match), ":")
	n := len(parts)
	if n == 6 {
		for n > 0 && parts[n-1] == "*" {
			n--
		}
	} else if n < 3 || n > 5 {
		return "", "", fmt.Errorf("invalid MAC or prefix")
	}
	if n < 3 || n > 6 {
		return "", "", fmt.Errorf("invalid MAC prefix length")
	}
	nonzero := false
	for i := 0; i < n; i++ {
		if len(parts[i]) != 2 {
			return "", "", fmt.Errorf("invalid MAC byte")
		}
		b, err := hex.DecodeString(parts[i])
		if err != nil {
			return "", "", fmt.Errorf("invalid MAC byte")
		}
		if i == 0 && b[0]&1 != 0 {
			return "", "", fmt.Errorf("multicast MAC is not allowed")
		}
		nonzero = nonzero || b[0] != 0
	}
	if !nonzero {
		return "", "", fmt.Errorf("zero MAC or prefix is not allowed")
	}
	level := "machine"
	if n < 6 {
		level = fmt.Sprintf("p%d", n)
		parts = append(parts[:n], make([]string, 6-n)...)
		for i := n; i < 6; i++ {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, ":"), level, nil
}

func normalizedBootDriverRules(rules []BootDriverRule) ([]BootDriverRule, error) {
	normalized := make([]BootDriverRule, 0, len(rules))
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if rule.Driver != "ipxe" && rule.Driver != "snp" {
			return nil, fmt.Errorf("invalid boot driver %q", rule.Driver)
		}
		match, _, err := canonicalDriverMatch(rule.Match)
		if err != nil {
			return nil, fmt.Errorf("invalid match %q: %w", rule.Match, err)
		}
		if seen[match] {
			return nil, fmt.Errorf("duplicate boot driver match %q", match)
		}
		seen[match] = true
		normalized = append(normalized, BootDriverRule{Match: match, Driver: rule.Driver})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Match < normalized[j].Match })
	return normalized, nil
}

func ValidateBootDriverRules(rules []BootDriverRule) error {
	_, err := normalizedBootDriverRules(rules)
	return err
}

func RenderDHCPHosts(rules []BootDriverRule) ([]byte, error) {
	normalized, err := normalizedBootDriverRules(rules)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, rule := range normalized {
		_, level, _ := canonicalDriverMatch(rule.Match)
		fmt.Fprintf(&b, "%s,set:omb-driver-%s-%s\n", rule.Match, level, rule.Driver)
	}
	return []byte(b.String()), nil
}
