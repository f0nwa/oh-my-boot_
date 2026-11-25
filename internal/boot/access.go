package boot

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

const (
	AccessModeSubnets = "subnets"
	AccessModeAny     = "any"
)

var defaultAllowedPrefix = netip.MustParsePrefix("10.30.0.0/24")

// ClientAccessPolicy defines which client IPs may fetch private boot scripts,
// answer files, autoinstall seeds, and Ubuntu installation images.
type ClientAccessPolicy struct {
	Mode    string   `json:"mode"`
	Subnets []string `json:"subnets,omitempty"`
}

// AccessPolicyManager stores the active client access policy in memory and
// persists updates atomically to disk.
type AccessPolicyManager struct {
	mu       sync.RWMutex
	path     string
	policy   ClientAccessPolicy
	prefixes []netip.Prefix
}

// NewAccessPolicyManager loads the persisted policy from path, falling back to
// envAllowed (OMB_BOOT_ALLOWED_SUBNETS) and finally the legacy default subnet.
func NewAccessPolicyManager(path, envAllowed string) *AccessPolicyManager {
	m := &AccessPolicyManager{
		path:     path,
		policy:   ClientAccessPolicy{Mode: AccessModeSubnets, Subnets: []string{defaultAllowedPrefix.String()}},
		prefixes: []netip.Prefix{defaultAllowedPrefix},
	}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var saved ClientAccessPolicy
			if err := json.Unmarshal(data, &saved); err == nil {
				if normalized, prefixes, err := validateAccessPolicy(saved); err == nil {
					m.policy = normalized
					m.prefixes = prefixes
					return m
				}
			}
		}
	}
	if policy, prefixes, ok := parseEnvAccessPolicy(envAllowed); ok {
		m.policy = policy
		m.prefixes = prefixes
	}
	return m
}

func parseEnvAccessPolicy(raw string) (ClientAccessPolicy, []netip.Prefix, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ClientAccessPolicy{}, nil, false
	}
	if raw == "*" || strings.EqualFold(raw, AccessModeAny) {
		return ClientAccessPolicy{Mode: AccessModeAny}, nil, true
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	})
	normalized, prefixes, err := validateAccessPolicy(ClientAccessPolicy{
		Mode:    AccessModeSubnets,
		Subnets: fields,
	})
	if err != nil {
		return ClientAccessPolicy{}, nil, false
	}
	return normalized, prefixes, true
}

func validateAccessPolicy(p ClientAccessPolicy) (ClientAccessPolicy, []netip.Prefix, error) {
	switch p.Mode {
	case AccessModeAny:
		return ClientAccessPolicy{Mode: AccessModeAny}, nil, nil
	case AccessModeSubnets:
		if len(p.Subnets) == 0 || len(p.Subnets) > 65 {
			return ClientAccessPolicy{}, nil, fmt.Errorf("subnets mode requires 1..65 IPv4 CIDR entries")
		}
		out := make([]string, 0, len(p.Subnets))
		prefixes := make([]netip.Prefix, 0, len(p.Subnets))
		seen := make(map[string]bool, len(p.Subnets))
		for _, raw := range p.Subnets {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
			if err != nil || !prefix.Addr().Is4() || prefix.Bits() < 1 || prefix.Bits() > 32 || prefix != prefix.Masked() {
				return ClientAccessPolicy{}, nil, fmt.Errorf("invalid IPv4 CIDR %q", raw)
			}
			s := prefix.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
				prefixes = append(prefixes, prefix)
			}
		}
		return ClientAccessPolicy{Mode: AccessModeSubnets, Subnets: out}, prefixes, nil
	default:
		return ClientAccessPolicy{}, nil, fmt.Errorf("invalid access mode %q", p.Mode)
	}
}

// Current returns a copy of the active access policy.
func (m *AccessPolicyManager) Current() ClientAccessPolicy {
	if m == nil {
		return ClientAccessPolicy{Mode: AccessModeSubnets, Subnets: []string{defaultAllowedPrefix.String()}}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := ClientAccessPolicy{Mode: m.policy.Mode}
	if len(m.policy.Subnets) > 0 {
		cp.Subnets = append([]string(nil), m.policy.Subnets...)
	}
	return cp
}

// Allows reports whether remoteAddr (host:port) is permitted by the active policy.
func (m *AccessPolicyManager) Allows(remoteAddr string) bool {
	client, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return false
	}
	addr := client.Addr().Unmap()
	if m == nil {
		return defaultAllowedPrefix.Contains(addr)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.policy.Mode == AccessModeAny {
		return true
	}
	for _, prefix := range m.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// Set validates, atomically persists, and activates a new client access policy.
func (m *AccessPolicyManager) Set(policy ClientAccessPolicy) error {
	if m == nil {
		return fmt.Errorf("access policy manager is unavailable")
	}
	normalized, prefixes, err := validateAccessPolicy(policy)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.path != "" {
		data, err := json.Marshal(normalized)
		if err != nil {
			return err
		}
		dir := filepath.Dir(m.path)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".access-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if err := tmp.Chmod(0o600); err != nil {
			tmp.Close()
			return err
		}
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), m.path); err != nil {
			return err
		}
	}
	m.policy = normalized
	m.prefixes = prefixes
	return nil
}
