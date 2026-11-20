package control

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"omb/internal/control/ldapclient"
)

const (
	adAccountDisableFilter = "(!(userAccountControl:1.2.840.113556.1.4.803:=2))"
	adInChainRule          = "1.2.840.113556.1.4.1941"
)

func (s *Server) authenticateLDAP(ctx context.Context, username, password string) (ExternalIdentity, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return ExternalIdentity{}, errors.New("empty credentials")
	}
	rec, cfg, sec, err := s.loadLDAPConfigAndSecrets(ctx)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if !rec.Enabled {
		return ExternalIdentity{}, errProviderDisabled
	}
	return authenticateLDAPWithConfig(ctx, cfg, sec, username, password)
}

func authenticateLDAPWithConfig(ctx context.Context, cfg LDAPConfig, sec LDAPSecrets, username, password string) (ExternalIdentity, error) {
	if err := validateLDAPConfig(cfg, sec); err != nil {
		return ExternalIdentity{}, err
	}
	svc, err := ldapclient.Dial(ctx, ldapclient.DialOptions{
		ServerURL: cfg.ServerURL,
		TLSMode:   cfg.TLSMode,
		CAPEM:     cfg.CAPEM,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return ExternalIdentity{}, err
	}
	defer svc.Close()

	if err := svc.Bind(ctx, cfg.BindDN, sec.BindPassword); err != nil {
		return ExternalIdentity{}, fmt.Errorf("ldap service bind: %w", err)
	}

	entry, err := findActiveLDAPUserByLogin(ctx, svc, cfg, username)
	if err != nil {
		return ExternalIdentity{}, err
	}

	// Verify user password via separate TLS user bind.
	userConn, err := ldapclient.Dial(ctx, ldapclient.DialOptions{
		ServerURL: cfg.ServerURL,
		TLSMode:   cfg.TLSMode,
		CAPEM:     cfg.CAPEM,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return ExternalIdentity{}, err
	}
	defer userConn.Close()
	if err := userConn.Bind(ctx, entry.DN, password); err != nil {
		return ExternalIdentity{}, errors.New("invalid credentials")
	}

	role, err := resolveLDAPUserRole(ctx, svc, cfg, entry)
	if err != nil {
		return ExternalIdentity{}, err
	}
	if !Allowed(role, "view") {
		return ExternalIdentity{}, errors.New("no allowed role")
	}

	extID, err := extractLDAPExternalID(cfg, entry)
	if err != nil {
		return ExternalIdentity{}, err
	}
	loginName := entry.FirstString(cfg.LoginAttr)
	if loginName == "" {
		loginName = username
	}
	displayName := entry.FirstString("displayName")
	if displayName == "" {
		displayName = entry.FirstString("cn")
	}
	return ExternalIdentity{
		Source:      AuthSourceLDAP,
		ExternalID:  extID,
		Username:    loginName,
		DisplayName: displayName,
		Role:        role,
	}, nil
}

func (s *Server) checkLDAPUserLive(ctx context.Context, externalID string) (ExternalUserStatus, error) {
	rec, cfg, sec, err := s.loadLDAPConfigAndSecrets(ctx)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	if !rec.Enabled {
		return ExternalUserStatus{}, errProviderDisabled
	}
	return checkLDAPUserWithConfig(ctx, cfg, sec, externalID)
}

func checkLDAPUserWithConfig(ctx context.Context, cfg LDAPConfig, sec LDAPSecrets, externalID string) (ExternalUserStatus, error) {
	if err := validateLDAPConfig(cfg, sec); err != nil {
		return ExternalUserStatus{}, err
	}
	dirPrefix := cfg.DirectoryID + ":"
	if !strings.HasPrefix(externalID, dirPrefix) {
		return ExternalUserStatus{Active: false}, nil
	}
	rawID := strings.TrimPrefix(externalID, dirPrefix)
	if rawID == "" {
		return ExternalUserStatus{Active: false}, nil
	}

	svc, err := ldapclient.Dial(ctx, ldapclient.DialOptions{
		ServerURL: cfg.ServerURL,
		TLSMode:   cfg.TLSMode,
		CAPEM:     cfg.CAPEM,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return ExternalUserStatus{}, err
	}
	defer svc.Close()
	if err := svc.Bind(ctx, cfg.BindDN, sec.BindPassword); err != nil {
		return ExternalUserStatus{}, fmt.Errorf("ldap service bind: %w", err)
	}

	var idFilter string
	if strings.EqualFold(cfg.IDAttr, "objectGUID") {
		guidBytes, err := ldapclient.ParseADGUID(rawID)
		if err != nil {
			return ExternalUserStatus{Active: false}, nil
		}
		idFilter = fmt.Sprintf("(objectGUID=%s)", ldapclient.EscapeBytesFilter(guidBytes))
	} else {
		idFilter = fmt.Sprintf("(%s=%s)", cfg.IDAttr, ldapclient.EscapeFilter(rawID))
	}
	filter := fmt.Sprintf("(&%s%s)", adAccountDisableFilter, idFilter)
	entries, err := svc.Search(ctx, cfg.BaseDN, ldapclient.ScopeWholeSubtree, filter,
		[]string{cfg.LoginAttr, cfg.IDAttr, "displayName", "cn", "memberOf"}, 2)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	if len(entries) != 1 {
		return ExternalUserStatus{Active: false}, nil
	}
	entry := entries[0]
	role, err := resolveLDAPUserRole(ctx, svc, cfg, entry)
	if err != nil {
		return ExternalUserStatus{}, err
	}
	if !Allowed(role, "view") {
		return ExternalUserStatus{Active: false}, nil
	}
	display := entry.FirstString("displayName")
	if display == "" {
		display = entry.FirstString("cn")
	}
	return ExternalUserStatus{
		Active:      true,
		Role:        role,
		Username:    entry.FirstString(cfg.LoginAttr),
		DisplayName: display,
	}, nil
}

// testLDAPConnection validates TLS, service bind, and optionally resolves a test username.
func testLDAPConnection(ctx context.Context, cfg LDAPConfig, sec LDAPSecrets, testUsername string) (string, error) {
	if err := validateLDAPConfig(cfg, sec); err != nil {
		return "", err
	}
	start := time.Now()
	svc, err := ldapclient.Dial(ctx, ldapclient.DialOptions{
		ServerURL: cfg.ServerURL,
		TLSMode:   cfg.TLSMode,
		CAPEM:     cfg.CAPEM,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return "", err
	}
	defer svc.Close()
	if err := svc.Bind(ctx, cfg.BindDN, sec.BindPassword); err != nil {
		return "", fmt.Errorf("ошибка bind служебной учётки: %w", err)
	}
	ms := time.Since(start).Milliseconds()
	testUsername = strings.TrimSpace(testUsername)
	if testUsername == "" {
		return fmt.Sprintf("TLS и bind служебной учётки успешны (%d мс)", ms), nil
	}
	entry, err := findActiveLDAPUserByLogin(ctx, svc, cfg, testUsername)
	if err != nil {
		return "", fmt.Errorf("поиск пользователя «%s»: %w", testUsername, err)
	}
	extID, err := extractLDAPExternalID(cfg, entry)
	if err != nil {
		return "", err
	}
	role, err := resolveLDAPUserRole(ctx, svc, cfg, entry)
	if err != nil {
		return "", err
	}
	roleText := role
	if roleText == "" {
		roleText = "нет подходящей группы"
	}
	return fmt.Sprintf("OK (%d мс): найден %s (ID=%s), роль=%s", ms, entry.DN, extID, roleText), nil
}

func validateLDAPConfig(cfg LDAPConfig, sec LDAPSecrets) error {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return errors.New("не указан адрес LDAP-сервера")
	}
	if cfg.TLSMode != "ldaps" && cfg.TLSMode != "starttls" {
		return errors.New("поддерживаются только режимы LDAPS или StartTLS")
	}
	if strings.TrimSpace(cfg.BindDN) == "" || sec.BindPassword == "" {
		return errors.New("не указаны данные служебной учётной записи")
	}
	if strings.TrimSpace(cfg.BaseDN) == "" {
		return errors.New("не указана база поиска (Base DN)")
	}
	if strings.TrimSpace(cfg.GroupAdminDN) == "" && strings.TrimSpace(cfg.GroupOperatorDN) == "" && strings.TrimSpace(cfg.GroupObserverDN) == "" {
		return errors.New("укажите хотя бы одну группу AD для ролей")
	}
	return nil
}

func findActiveLDAPUserByLogin(ctx context.Context, svc *ldapclient.Client, cfg LDAPConfig, username string) (ldapclient.Entry, error) {
	attr := cfg.LoginAttr
	if attr == "" {
		attr = "sAMAccountName"
	}
	escaped := ldapclient.EscapeFilter(username)
	var match string
	if strings.Contains(username, "@") && strings.EqualFold(attr, "sAMAccountName") {
		match = fmt.Sprintf("(|(sAMAccountName=%s)(userPrincipalName=%s))", escaped, escaped)
	} else {
		match = fmt.Sprintf("(%s=%s)", attr, escaped)
	}
	filter := fmt.Sprintf("(&(objectCategory=person)(objectClass=user)%s%s)", adAccountDisableFilter, match)
	entries, err := svc.Search(ctx, cfg.BaseDN, ldapclient.ScopeWholeSubtree, filter,
		[]string{attr, cfg.IDAttr, "displayName", "cn", "memberOf"}, 2)
	if err != nil {
		return ldapclient.Entry{}, err
	}
	if len(entries) == 0 {
		return ldapclient.Entry{}, errors.New("user not found or disabled")
	}
	if len(entries) > 1 {
		return ldapclient.Entry{}, errors.New("ambiguous LDAP search result")
	}
	return entries[0], nil
}

func resolveLDAPUserRole(ctx context.Context, svc *ldapclient.Client, cfg LDAPConfig, entry ldapclient.Entry) (string, error) {
	groups := []struct {
		role string
		dn   string
	}{
		{"admin", strings.TrimSpace(cfg.GroupAdminDN)},
		{"operator", strings.TrimSpace(cfg.GroupOperatorDN)},
		{"observer", strings.TrimSpace(cfg.GroupObserverDN)},
	}

	if !cfg.NestedGroups {
		memberOf := map[string]bool{}
		for _, v := range entry.Values("memberOf") {
			memberOf[strings.ToLower(strings.TrimSpace(string(v)))] = true
		}
		var matched []string
		for _, g := range groups {
			if g.dn != "" && memberOf[strings.ToLower(g.dn)] {
				matched = append(matched, g.role)
			}
		}
		return HighestRole(matched...), nil
	}

	// Nested groups check via AD LDAP_MATCHING_RULE_IN_CHAIN against each configured group DN.
	escapedUserDN := ldapclient.EscapeFilter(entry.DN)
	filter := fmt.Sprintf("(member:%s:=%s)", adInChainRule, escapedUserDN)
	var matched []string
	for _, g := range groups {
		if g.dn == "" {
			continue
		}
		res, err := svc.Search(ctx, g.dn, ldapclient.ScopeBaseObject, filter, []string{"dn"}, 1)
		if err != nil {
			// Fallback to direct memberOf if the directory does not support IN_CHAIN on base search.
			for _, v := range entry.Values("memberOf") {
				if strings.EqualFold(strings.TrimSpace(string(v)), g.dn) {
					matched = append(matched, g.role)
					break
				}
			}
			continue
		}
		if len(res) > 0 {
			matched = append(matched, g.role)
		}
	}
	return HighestRole(matched...), nil
}

func extractLDAPExternalID(cfg LDAPConfig, entry ldapclient.Entry) (string, error) {
	idAttr := cfg.IDAttr
	if idAttr == "" {
		idAttr = "objectGUID"
	}
	raw := entry.FirstBytes(idAttr)
	if len(raw) == 0 {
		return "", fmt.Errorf("LDAP entry missing immutable ID attribute %q", idAttr)
	}
	var idVal string
	if strings.EqualFold(idAttr, "objectGUID") && len(raw) == 16 {
		formatted, err := ldapclient.FormatADGUID(raw)
		if err != nil {
			return "", err
		}
		idVal = formatted
	} else {
		idVal = strings.TrimSpace(string(raw))
	}
	return cfg.DirectoryID + ":" + idVal, nil
}
