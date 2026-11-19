package control

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func fileServerTestStore(t *testing.T) Store {
	t.Helper()
	if url := os.Getenv("OMB_TEST_DATABASE_URL"); url != "" {
		pool, err := pgxpool.New(context.Background(), url)
		if err != nil {
			t.Fatalf("connect to OMB_TEST_DATABASE_URL: %v", err)
		}
		t.Cleanup(pool.Close)
		sql, err := os.ReadFile("../../deploy/sql/control.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
			t.Fatal(err)
		}
		return Store{Pool: pool}
	}
	return installationTestStore(t)
}

func TestValidateFileServerSettings(t *testing.T) {
	tests := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{name: "port 1023 too low", port: 1023, wantErr: true},
		{name: "port 1024 valid boundary", port: 1024, wantErr: false},
		{name: "port 2022 default", port: 2022, wantErr: false},
		{name: "port 65535 valid boundary", port: 65535, wantErr: false},
		{name: "port 65536 too high", port: 65536, wantErr: true},
		{name: "port 0 invalid", port: 0, wantErr: true},
		{name: "negative port", port: -1, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := FileServerSettings{
				SMBEnabled:  true,
				SFTPEnabled: true,
				SFTPPort:    tc.port,
			}
			err := ValidateFileServerSettings(cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateFileServerSettings(%+v) err=%v, wantErr=%v", cfg, err, tc.wantErr)
			}
		})
	}
}

func TestValidateFileServerAccessMode(t *testing.T) {
	tests := []struct {
		mode  string
		valid bool
	}{
		{mode: FSAccessNone, valid: true},
		{mode: FSAccessRO, valid: true},
		{mode: FSAccessRW, valid: true},
		{mode: "invalid", valid: false},
		{mode: "", valid: false},
		{mode: "read", valid: false},
		{mode: "write", valid: false},
		{mode: "all", valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			got := ValidFSAccessMode(tc.mode)
			if got != tc.valid {
				t.Fatalf("ValidFSAccessMode(%q)=%v, want %v", tc.mode, got, tc.valid)
			}
		})
	}
}

func TestFileServerStoreSettings(t *testing.T) {
	ctx := context.Background()
	store := fileServerTestStore(t)

	// Default settings seeded by control.sql
	cfg, err := store.FileServerSettings(ctx)
	if err != nil {
		t.Fatalf("FileServerSettings: %v", err)
	}
	if cfg.SMBEnabled || cfg.SFTPEnabled || cfg.SFTPPort != DefaultSFTPPort {
		t.Fatalf("unexpected default settings: %+v", cfg)
	}

	// Update settings with valid values
	newCfg := FileServerSettings{
		SMBEnabled:  true,
		SFTPEnabled: true,
		SFTPPort:    2222,
	}
	if err := store.SetFileServerSettings(ctx, newCfg); err != nil {
		t.Fatalf("SetFileServerSettings: %v", err)
	}

	updated, err := store.FileServerSettings(ctx)
	if err != nil {
		t.Fatalf("FileServerSettings after update: %v", err)
	}
	if updated != newCfg {
		t.Fatalf("got settings %+v, want %+v", updated, newCfg)
	}

	// Invalid settings rejected
	invalidCfg := FileServerSettings{
		SMBEnabled:  true,
		SFTPEnabled: true,
		SFTPPort:    999,
	}
	if err := store.SetFileServerSettings(ctx, invalidCfg); err == nil {
		t.Fatal("expected error for invalid port 999, got nil")
	}
}

func TestFileServerStoreUserLifecycle(t *testing.T) {
	ctx := context.Background()
	store := fileServerTestStore(t)

	// Create users
	if err := store.CreateInitialAdmin(ctx, "admin_user", "AdminSecret123!"); err != nil {
		t.Fatalf("CreateInitialAdmin: %v", err)
	}
	if err := store.CreateUser(ctx, "op_user", "OperatorSecret123!", "operator"); err != nil {
		t.Fatalf("CreateUser op: %v", err)
	}
	if err := store.CreateUser(ctx, "disabled_user", "DisabledSecret123!", "observer"); err != nil {
		t.Fatalf("CreateUser disabled: %v", err)
	}

	adminID, err := store.UserID(ctx, "admin_user")
	if err != nil {
		t.Fatalf("UserID admin: %v", err)
	}
	opID, err := store.UserID(ctx, "op_user")
	if err != nil {
		t.Fatalf("UserID op: %v", err)
	}
	disabledID, err := store.UserID(ctx, "disabled_user")
	if err != nil {
		t.Fatalf("UserID disabled: %v", err)
	}
	// Disable disabled_user
	if err := store.ChangeUser(ctx, disabledID, adminID, "observer", false); err != nil {
		t.Fatalf("ChangeUser disable: %v", err)
	}

	// 1. Initial state in FileServerUsers
	fsUsers, err := store.FileServerUsers(ctx)
	if err != nil {
		t.Fatalf("FileServerUsers: %v", err)
	}
	if len(fsUsers) != 3 {
		t.Fatalf("expected 3 users, got %d", len(fsUsers))
	}
	for _, u := range fsUsers {
		if u.Mode != FSAccessNone || u.HasPassword {
			t.Fatalf("user %+v initial state should be none without password", u)
		}
	}

	// 2. Enabling ro without password returns errFSPasswordRequired
	_, err = store.SetUserFileAccess(ctx, opID, FSAccessRO, "")
	if !errors.Is(err, errFSPasswordRequired) {
		t.Fatalf("expected errFSPasswordRequired, got %v", err)
	}

	// 3. Invalid mode rejected
	_, err = store.SetUserFileAccess(ctx, opID, "invalid_mode", "SomeSecret1234!")
	if !errors.Is(err, errInvalidFSAccessMode) {
		t.Fatalf("expected errInvalidFSAccessMode, got %v", err)
	}

	// 4. Short password rejected
	_, err = store.SetUserFileAccess(ctx, opID, FSAccessRW, "short")
	if err == nil {
		t.Fatal("expected error for short password, got nil")
	}

	// 5. Enabling rw with 12+ char password stores yescrypt hash and returns username
	username, err := store.SetUserFileAccess(ctx, opID, FSAccessRW, "ValidFSPassword123!")
	if err != nil {
		t.Fatalf("SetUserFileAccess: %v", err)
	}
	if username != "op_user" {
		t.Fatalf("expected username op_user, got %q", username)
	}

	// AuthenticateFSUser succeeds with correct password
	authUser, err := store.AuthenticateFSUser(ctx, "op_user", "ValidFSPassword123!")
	if err != nil {
		t.Fatalf("AuthenticateFSUser: %v", err)
	}
	if authUser.Username != "op_user" || authUser.FSAccessMode != FSAccessRW || !authUser.HasFSPassword || authUser.FSPasswordChangedAt == nil {
		t.Fatalf("unexpected authUser: %+v", authUser)
	}

	// AuthenticateFSUser rejects wrong password
	if _, err := store.AuthenticateFSUser(ctx, "op_user", "WrongPassword123!"); err == nil {
		t.Fatal("expected auth failure with wrong password, got nil")
	}

	// 6. Switching rw -> ro with empty password keeps hash
	username, err = store.SetUserFileAccess(ctx, opID, FSAccessRO, "")
	if err != nil {
		t.Fatalf("SetUserFileAccess rw->ro with empty password: %v", err)
	}
	if username != "op_user" {
		t.Fatalf("expected username op_user, got %q", username)
	}

	authUser, err = store.AuthenticateFSUser(ctx, "op_user", "ValidFSPassword123!")
	if err != nil {
		t.Fatalf("AuthenticateFSUser after ro switch: %v", err)
	}
	if authUser.FSAccessMode != FSAccessRO {
		t.Fatalf("expected mode ro, got %s", authUser.FSAccessMode)
	}

	// 7. SetOwnFilePassword works for ro user
	username, err = store.SetOwnFilePassword(ctx, opID, "NewOwnPassword123!")
	if err != nil {
		t.Fatalf("SetOwnFilePassword: %v", err)
	}
	if username != "op_user" {
		t.Fatalf("expected username op_user, got %q", username)
	}

	// Old password rejected, new password accepted
	if _, err := store.AuthenticateFSUser(ctx, "op_user", "ValidFSPassword123!"); err == nil {
		t.Fatal("expected old password to fail, got nil")
	}
	if _, err := store.AuthenticateFSUser(ctx, "op_user", "NewOwnPassword123!"); err != nil {
		t.Fatalf("AuthenticateFSUser with new password: %v", err)
	}

	// 8. SetOwnFilePassword rejected for user with FSAccessNone
	_, err = store.SetOwnFilePassword(ctx, adminID, "NewAdminFSPass123!")
	if !errors.Is(err, errFSNotEnabled) {
		t.Fatalf("expected errFSNotEnabled for admin with none mode, got %v", err)
	}

	// 9. Disabling none clears hash and timestamp
	username, err = store.SetUserFileAccess(ctx, opID, FSAccessNone, "")
	if err != nil {
		t.Fatalf("SetUserFileAccess none: %v", err)
	}
	if username != "op_user" {
		t.Fatalf("expected username op_user, got %q", username)
	}

	// AuthenticateFSUser rejects disabled mode
	if _, err := store.AuthenticateFSUser(ctx, "op_user", "NewOwnPassword123!"); !errors.Is(err, errFSNotEnabled) && err == nil {
		t.Fatalf("expected errFSNotEnabled, got %v", err)
	}

	// Switching to ro now requires password again
	if _, err := store.SetUserFileAccess(ctx, opID, FSAccessRO, ""); !errors.Is(err, errFSPasswordRequired) {
		t.Fatalf("expected errFSPasswordRequired after reset to none, got %v", err)
	}

	// 10. AuthenticateFSUser rejects disabled user (Enabled=false)
	_, err = store.SetUserFileAccess(ctx, disabledID, FSAccessRW, "DisabledUserFSPass123!")
	if err != nil {
		t.Fatalf("SetUserFileAccess for disabled user: %v", err)
	}
	if _, err := store.AuthenticateFSUser(ctx, "disabled_user", "DisabledUserFSPass123!"); err == nil {
		t.Fatal("expected failure authenticating disabled user, got nil")
	}
}

func TestFileServerStoreUsersAndSessionIncludeFSFields(t *testing.T) {
	ctx := context.Background()
	store := fileServerTestStore(t)

	if err := store.CreateInitialAdmin(ctx, "admin_user", "AdminSecret123!"); err != nil {
		t.Fatalf("CreateInitialAdmin: %v", err)
	}
	adminID, err := store.UserID(ctx, "admin_user")
	if err != nil {
		t.Fatalf("UserID: %v", err)
	}

	// Assign RW access and password
	if _, err := store.SetUserFileAccess(ctx, adminID, FSAccessRW, "AdminFilePass123!"); err != nil {
		t.Fatalf("SetUserFileAccess: %v", err)
	}

	// Test Users()
	users, err := store.Users(ctx)
	if err != nil {
		t.Fatalf("Users(): %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	u := users[0]
	if u.FSAccessMode != FSAccessRW || !u.HasFSPassword || u.FSPasswordChangedAt == nil {
		t.Fatalf("Users() row has wrong FS fields: %+v", u)
	}

	// Test Login()
	logged, token, err := store.Login(ctx, "admin_user", "AdminSecret123!")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if logged.FSAccessMode != FSAccessRW || !logged.HasFSPassword || logged.FSPasswordChangedAt == nil {
		t.Fatalf("Login() returned wrong FS fields: %+v", logged)
	}

	// Test Session()
	sessionUser, err := store.Session(ctx, token)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if sessionUser.FSAccessMode != FSAccessRW || !sessionUser.HasFSPassword || sessionUser.FSPasswordChangedAt == nil {
		t.Fatalf("Session() returned wrong FS fields: %+v", sessionUser)
	}
}
