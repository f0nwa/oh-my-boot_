package control

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPasswordPolicyStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)

	// Default policy is seeded by control.sql.
	pol, err := store.PasswordPolicy(ctx)
	if err != nil {
		t.Fatalf("PasswordPolicy default: %v", err)
	}
	if pol.MaxAgeDays != 0 || pol.WarnDays != 7 {
		t.Fatalf("unexpected default policy: %+v", pol)
	}

	if err := store.SetPasswordPolicy(ctx, PasswordPolicy{MaxAgeDays: 90, WarnDays: 10}); err != nil {
		t.Fatalf("SetPasswordPolicy: %v", err)
	}
	pol, err = store.PasswordPolicy(ctx)
	if err != nil || pol.MaxAgeDays != 90 || pol.WarnDays != 10 {
		t.Fatalf("updated policy=%+v err=%v", pol, err)
	}

	// CreateInitialAdmin creates an active admin without must_change_password.
	if err := store.CreateInitialAdmin(ctx, "rootadmin", "AdminSecret123!"); err != nil {
		t.Fatalf("CreateInitialAdmin: %v", err)
	}
	// CreateUserWithPolicy with mustChange=true and custom 30d.
	if err := store.CreateUserWithPolicy(ctx, "temp_op", "TempSecret123!", "operator", PasswordModeCustom, 30, true); err != nil {
		t.Fatalf("CreateUserWithPolicy: %v", err)
	}
	// CreateUserWithPolicy with never expires.
	if err := store.CreateUserWithPolicy(ctx, "svc_obs", "SvcSecret1234!", "observer", PasswordModeNever, 0, false); err != nil {
		t.Fatalf("CreateUserWithPolicy never: %v", err)
	}

	users, err := store.Users(ctx)
	if err != nil || len(users) != 3 {
		t.Fatalf("Users=%+v err=%v", users, err)
	}
	var rootUser, tempUser, svcUser User
	for _, u := range users {
		switch u.Username {
		case "rootadmin":
			rootUser = u
		case "temp_op":
			tempUser = u
		case "svc_obs":
			svcUser = u
		}
	}
	if rootUser.PasswordChangedAt.IsZero() || rootUser.MustChangePassword || rootUser.PasswordPolicyMode != PasswordModePolicy {
		t.Fatalf("rootUser=%+v", rootUser)
	}
	if !tempUser.MustChangePassword || tempUser.PasswordPolicyMode != PasswordModeCustom || tempUser.PasswordMaxAgeDays != 30 {
		t.Fatalf("tempUser=%+v", tempUser)
	}
	if svcUser.MustChangePassword || svcUser.PasswordPolicyMode != PasswordModeNever || svcUser.PasswordMaxAgeDays != 0 {
		t.Fatalf("svcUser=%+v", svcUser)
	}

	// Login & Session expose password fields.
	logged, token, err := store.Login(ctx, "temp_op", "TempSecret123!")
	if err != nil || !logged.MustChangePassword || logged.PasswordPolicyMode != PasswordModeCustom || logged.PasswordMaxAgeDays != 30 {
		t.Fatalf("Login=%+v err=%v", logged, err)
	}
	sessUser, err := store.Session(ctx, token)
	if err != nil || !sessUser.MustChangePassword || sessUser.PasswordPolicyMode != PasswordModeCustom || sessUser.PasswordMaxAgeDays != 30 {
		t.Fatalf("Session=%+v err=%v", sessUser, err)
	}

	// ChangeOwnPassword rejects reusing the current password.
	if err := store.ChangeOwnPassword(ctx, tempUser.ID, token, "TempSecret123!", "TempSecret123!"); !errors.Is(err, errSamePassword) {
		t.Fatalf("expected errSamePassword, got %v", err)
	}
	// Backdate password_changed_at so we verify ChangeOwnPassword refreshes it and clears must_change_password.
	oldTime := time.Now().Add(-40 * 24 * time.Hour)
	if _, err := store.Pool.Exec(ctx, "UPDATE users SET password_changed_at=$2 WHERE id=$1", tempUser.ID, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := store.ChangeOwnPassword(ctx, tempUser.ID, token, "TempSecret123!", "NewOperatorPass123!"); err != nil {
		t.Fatalf("ChangeOwnPassword: %v", err)
	}
	sessAfter, err := store.Session(ctx, token)
	if err != nil || sessAfter.MustChangePassword || !sessAfter.PasswordChangedAt.After(oldTime.Add(24*time.Hour)) {
		t.Fatalf("Session after change=%+v err=%v", sessAfter, err)
	}

	// SetUserPasswordPolicy cannot force must_change_password on actor itself, but can set own mode.
	forceTrue := true
	if err := store.SetUserPasswordPolicy(ctx, rootUser.ID, rootUser.ID, PasswordModeNever, 0, &forceTrue); err != nil {
		t.Fatalf("SetUserPasswordPolicy self: %v", err)
	}
	if err := store.SetUserPasswordPolicy(ctx, tempUser.ID, rootUser.ID, PasswordModePolicy, 0, &forceTrue); err != nil {
		t.Fatalf("SetUserPasswordPolicy other: %v", err)
	}
	users, err = store.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.ID == rootUser.ID && (u.PasswordPolicyMode != PasswordModeNever || u.MustChangePassword) {
			t.Fatalf("self policy update unexpected: %+v", u)
		}
		if u.ID == tempUser.ID && (u.PasswordPolicyMode != PasswordModePolicy || u.PasswordMaxAgeDays != 0 || !u.MustChangePassword) {
			t.Fatalf("other policy update unexpected: %+v", u)
		}
	}

	// ResetPassword (used by CLI) clears must_change_password and refreshes password_changed_at.
	if err := store.ResetPassword(ctx, tempUser.ID, "CliResetPass123!"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	_, token2, err := store.Login(ctx, "temp_op", "CliResetPass123!")
	if err != nil {
		t.Fatalf("Login after ResetPassword: %v", err)
	}
	sessReset, err := store.Session(ctx, token2)
	if err != nil || sessReset.MustChangePassword {
		t.Fatalf("ResetPassword should clear must_change_password: %+v err=%v", sessReset, err)
	}

	// ResetPasswordWithOptions(..., true) sets must_change_password=true.
	if err := store.ResetPasswordWithOptions(ctx, tempUser.ID, "AdminResetPass123!", true); err != nil {
		t.Fatalf("ResetPasswordWithOptions: %v", err)
	}
	_, token3, err := store.Login(ctx, "temp_op", "AdminResetPass123!")
	if err != nil {
		t.Fatalf("Login after ResetPasswordWithOptions: %v", err)
	}
	sessForced, err := store.Session(ctx, token3)
	if err != nil || !sessForced.MustChangePassword {
		t.Fatalf("ResetPasswordWithOptions(true) must set must_change_password: %+v err=%v", sessForced, err)
	}
}
