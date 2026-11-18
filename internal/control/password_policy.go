package control

import (
	"fmt"
	"math"
	"time"
)

const (
	PasswordModePolicy = "policy"
	PasswordModeNever  = "never"
	PasswordModeCustom = "custom"

	maxPasswordPolicyDays = 3650
	maxPasswordWarnDays   = 90
	defaultWarnDays       = 7
)

// PasswordPolicy defines server-wide expiration rules for local accounts.
type PasswordPolicy struct {
	MaxAgeDays int
	WarnDays   int
}

// PasswordState summarizes the evaluated expiration and change status for one user.
type PasswordState struct {
	MustChange      bool
	Expired         bool
	ExpiringSoon    bool
	NeverExpires    bool
	EffectiveMaxAge int
	ExpiresAt       time.Time
	DaysLeft        int
	OverdueDays     int
	Mode            string
}

// RequiresChange reports whether the user must change their password before accessing OMB.
func (s PasswordState) RequiresChange() bool {
	return s.MustChange || s.Expired
}

// IsLocal reports whether the account authenticates with a local password.
// All accounts are currently local; future external providers (Keycloak/LDAP) will return false.
func (u User) IsLocal() bool {
	return true
}

// NormalizePasswordMode maps empty or unknown mode values to the default policy mode.
func NormalizePasswordMode(mode string) string {
	switch mode {
	case PasswordModeNever, PasswordModeCustom:
		return mode
	default:
		return PasswordModePolicy
	}
}

// ValidatePasswordPolicy validates server-wide policy bounds.
func ValidatePasswordPolicy(p PasswordPolicy) error {
	if p.MaxAgeDays < 0 || p.MaxAgeDays > maxPasswordPolicyDays {
		return fmt.Errorf("password max age must be between 0 and %d days", maxPasswordPolicyDays)
	}
	if p.WarnDays < 0 || p.WarnDays > maxPasswordWarnDays {
		return fmt.Errorf("password warn threshold must be between 0 and %d days", maxPasswordWarnDays)
	}
	return nil
}

// ValidateUserPasswordRule validates a per-user expiration rule.
func ValidateUserPasswordRule(mode string, customDays int) error {
	switch mode {
	case PasswordModePolicy, PasswordModeNever:
		if customDays != 0 {
			return fmt.Errorf("custom days must be zero for mode %q", mode)
		}
		return nil
	case PasswordModeCustom:
		if customDays < 1 || customDays > maxPasswordPolicyDays {
			return fmt.Errorf("custom password age must be between 1 and %d days", maxPasswordPolicyDays)
		}
		return nil
	default:
		return fmt.Errorf("invalid password policy mode")
	}
}

// EvaluatePasswordState computes expiration and mandatory change status for a user at now.
func EvaluatePasswordState(u User, policy PasswordPolicy, now time.Time) PasswordState {
	mode := NormalizePasswordMode(u.PasswordPolicyMode)
	if !u.IsLocal() {
		return PasswordState{NeverExpires: true, Mode: PasswordModeNever}
	}

	effective := 0
	switch mode {
	case PasswordModeNever:
		effective = 0
	case PasswordModeCustom:
		if u.PasswordMaxAgeDays > 0 {
			effective = u.PasswordMaxAgeDays
		}
	default:
		if policy.MaxAgeDays > 0 {
			effective = policy.MaxAgeDays
		}
	}

	st := PasswordState{
		MustChange:      u.MustChangePassword,
		NeverExpires:    effective == 0,
		EffectiveMaxAge: effective,
		Mode:            mode,
	}
	if effective == 0 {
		return st
	}

	if u.PasswordChangedAt.IsZero() {
		st.Expired = true
		return st
	}

	expiresAt := u.PasswordChangedAt.Add(time.Duration(effective) * 24 * time.Hour)
	st.ExpiresAt = expiresAt
	if !now.Before(expiresAt) {
		st.Expired = true
		st.OverdueDays = int(now.Sub(expiresAt) / (24 * time.Hour))
		return st
	}

	rem := expiresAt.Sub(now)
	st.DaysLeft = int(math.Ceil(rem.Hours() / 24))
	if policy.WarnDays > 0 && st.DaysLeft <= policy.WarnDays {
		st.ExpiringSoon = true
	}
	return st
}

func formatPasswordStatusLabel(lang string, st PasswordState) string {
	switch {
	case st.MustChange:
		return translate(lang, "Смена при следующем входе")
	case st.NeverExpires:
		return translate(lang, "Бессрочный")
	case st.Expired:
		if st.OverdueDays <= 0 {
			return translate(lang, "Истёк сегодня")
		}
		return fmt.Sprintf(translate(lang, "Истёк %d дн. назад"), st.OverdueDays)
	case st.ExpiringSoon:
		return fmt.Sprintf(translate(lang, "Истекает через %d дн."), st.DaysLeft)
	default:
		return fmt.Sprintf(translate(lang, "Действует ещё %d дн."), st.DaysLeft)
	}
}

func formatPasswordRuleLabel(lang string, u User, pol PasswordPolicy) string {
	switch NormalizePasswordMode(u.PasswordPolicyMode) {
	case PasswordModeNever:
		return translate(lang, "правило: бессрочный")
	case PasswordModeCustom:
		return fmt.Sprintf(translate(lang, "свой срок: %d дн."), u.PasswordMaxAgeDays)
	default:
		if pol.MaxAgeDays <= 0 {
			return translate(lang, "по политике (бессрочно)")
		}
		return fmt.Sprintf(translate(lang, "по политике (%d дн.)"), pol.MaxAgeDays)
	}
}

func formatPasswordPolicySummary(lang string, pol PasswordPolicy) string {
	if pol.MaxAgeDays <= 0 {
		return translate(lang, "Общая политика: бессрочно")
	}
	if pol.WarnDays <= 0 {
		return fmt.Sprintf(translate(lang, "Общая политика: %d дн."), pol.MaxAgeDays)
	}
	return fmt.Sprintf(translate(lang, "Общая политика: %d дн. · предупреждение за %d дн."), pol.MaxAgeDays, pol.WarnDays)
}

func formatPasswordWarnMessage(lang string, st PasswordState) string {
	if !st.ExpiringSoon {
		return ""
	}
	if !st.ExpiresAt.IsZero() {
		return fmt.Sprintf(translate(lang, "Срок действия вашего пароля истекает через %d дн. (до %s)."), st.DaysLeft, st.ExpiresAt.Local().Format("02.01.2006"))
	}
	return fmt.Sprintf(translate(lang, "Срок действия вашего пароля истекает через %d дн."), st.DaysLeft)
}
