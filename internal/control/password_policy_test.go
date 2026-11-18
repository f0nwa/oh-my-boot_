package control

import (
	"testing"
	"time"
)

func TestEvaluatePasswordState(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	t.Run("default zero policy never expires", func(t *testing.T) {
		u := User{ID: 1, Username: "admin", PasswordChangedAt: now.Add(-400 * 24 * time.Hour)}
		st := EvaluatePasswordState(u, PasswordPolicy{}, now)
		if !st.NeverExpires || st.Expired || st.ExpiringSoon || st.RequiresChange() || st.EffectiveMaxAge != 0 {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("never expires overrides global policy", func(t *testing.T) {
		u := User{
			ID:                 1,
			Username:           "admin",
			PasswordChangedAt:  now.Add(-200 * 24 * time.Hour),
			PasswordPolicyMode: PasswordModeNever,
		}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 90, WarnDays: 7}, now)
		if !st.NeverExpires || st.Expired || st.ExpiringSoon || st.RequiresChange() || st.EffectiveMaxAge != 0 {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("policy mode warns within warn_days", func(t *testing.T) {
		u := User{
			ID:                 2,
			Username:           "op",
			PasswordChangedAt:  now.Add(-85 * 24 * time.Hour),
			PasswordPolicyMode: PasswordModePolicy,
		}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 90, WarnDays: 7}, now)
		if st.NeverExpires || st.Expired || !st.ExpiringSoon || st.DaysLeft != 5 || st.RequiresChange() {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("policy mode with warn_days=0 does not warn", func(t *testing.T) {
		u := User{
			ID:                 2,
			Username:           "op",
			PasswordChangedAt:  now.Add(-89 * 24 * time.Hour),
			PasswordPolicyMode: PasswordModePolicy,
		}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 90, WarnDays: 0}, now)
		if st.ExpiringSoon || st.Expired || st.DaysLeft != 1 {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("policy mode expired at exact boundary", func(t *testing.T) {
		u := User{
			ID:                2,
			Username:          "op",
			PasswordChangedAt: now.Add(-90 * 24 * time.Hour),
		}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 90, WarnDays: 7}, now)
		if !st.Expired || !st.RequiresChange() || st.DaysLeft != 0 || st.OverdueDays != 0 {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("custom mode overrides zero or larger global policy", func(t *testing.T) {
		u := User{
			ID:                 3,
			Username:           "svc",
			PasswordChangedAt:  now.Add(-32 * 24 * time.Hour),
			PasswordPolicyMode: PasswordModeCustom,
			PasswordMaxAgeDays: 30,
		}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 0, WarnDays: 7}, now)
		if !st.Expired || !st.RequiresChange() || st.EffectiveMaxAge != 30 || st.OverdueDays != 2 {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("must_change_password forces change even for never expires", func(t *testing.T) {
		u := User{
			ID:                 4,
			Username:           "new",
			PasswordChangedAt:  now,
			PasswordPolicyMode: PasswordModeNever,
			MustChangePassword: true,
		}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 90, WarnDays: 7}, now)
		if !st.MustChange || !st.RequiresChange() || st.Expired {
			t.Fatalf("unexpected state: %+v", st)
		}
	})

	t.Run("zero changed timestamp expires when policy has limit", func(t *testing.T) {
		u := User{ID: 5, Username: "legacy"}
		st := EvaluatePasswordState(u, PasswordPolicy{MaxAgeDays: 30, WarnDays: 7}, now)
		if !st.Expired || !st.RequiresChange() {
			t.Fatalf("expected expired for zero PasswordChangedAt with max age: %+v", st)
		}
	})
}

func TestValidatePasswordPolicyAndRules(t *testing.T) {
	if err := ValidatePasswordPolicy(PasswordPolicy{MaxAgeDays: 0, WarnDays: 7}); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	if err := ValidatePasswordPolicy(PasswordPolicy{MaxAgeDays: 3650, WarnDays: 90}); err != nil {
		t.Fatalf("boundary policy rejected: %v", err)
	}
	for _, bad := range []PasswordPolicy{
		{MaxAgeDays: -1, WarnDays: 7},
		{MaxAgeDays: 3651, WarnDays: 7},
		{MaxAgeDays: 30, WarnDays: -1},
		{MaxAgeDays: 30, WarnDays: 91},
	} {
		if err := ValidatePasswordPolicy(bad); err == nil {
			t.Fatalf("expected error for invalid policy %+v", bad)
		}
	}

	if err := ValidateUserPasswordRule(PasswordModePolicy, 0); err != nil {
		t.Fatalf("policy rule rejected: %v", err)
	}
	if err := ValidateUserPasswordRule(PasswordModeNever, 0); err != nil {
		t.Fatalf("never rule rejected: %v", err)
	}
	if err := ValidateUserPasswordRule(PasswordModeCustom, 45); err != nil {
		t.Fatalf("custom rule rejected: %v", err)
	}
	for _, tc := range []struct {
		mode string
		days int
	}{
		{"unknown", 0},
		{PasswordModeCustom, 0},
		{PasswordModeCustom, -5},
		{PasswordModeCustom, 3651},
		{PasswordModePolicy, 30},
		{PasswordModeNever, 30},
	} {
		if err := ValidateUserPasswordRule(tc.mode, tc.days); err == nil {
			t.Fatalf("expected error for invalid rule (%q, %d)", tc.mode, tc.days)
		}
	}
}
