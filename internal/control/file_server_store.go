package control

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/openwall/yescrypt-go"
)

const (
	FSAccessNone    = "none"
	FSAccessRO      = "ro"
	FSAccessRW      = "rw"
	DefaultSFTPPort = 2022
)

var (
	errFSPasswordRequired  = errors.New("file server password is required")
	errInvalidFSAccessMode = errors.New("invalid file server access mode")
	errFSNotEnabled        = errors.New("file server access is not enabled")

	ErrFSPasswordRequired  = errFSPasswordRequired
	ErrInvalidFSAccessMode = errInvalidFSAccessMode
	ErrFSNotEnabled        = errFSNotEnabled
)

type FileServerSettings struct {
	SMBEnabled  bool `json:"smb_enabled"`
	SFTPEnabled bool `json:"sftp_enabled"`
	SFTPPort    int  `json:"sftp_port"`
}

type FileServerUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	Mode        string `json:"mode"`
	Enabled     bool   `json:"enabled"`
	HasPassword bool   `json:"has_password"`
}

type FileServerStore interface {
	FileServerSettings(context.Context) (FileServerSettings, error)
	SetFileServerSettings(context.Context, FileServerSettings) error
	SetUserFileAccess(ctx context.Context, id int64, mode, password string) (string, error)
	SetOwnFilePassword(ctx context.Context, id int64, password string) (string, error)
	AuthenticateFSUser(ctx context.Context, username, password string) (User, error)
	FileServerUsers(context.Context) ([]FileServerUser, error)
}

var _ FileServerStore = Store{}

func ValidateFileServerSettings(settings FileServerSettings) error {
	if settings.SFTPPort < 1024 || settings.SFTPPort > 65535 {
		return fmt.Errorf("sftp_port must be between 1024 and 65535")
	}
	return nil
}

func ValidFSAccessMode(mode string) bool {
	switch mode {
	case FSAccessNone, FSAccessRO, FSAccessRW:
		return true
	default:
		return false
	}
}

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func hashFSPassword(password string) (string, error) {
	if utf8.RuneCountInString(password) < 12 {
		return "", errors.New("password must have at least 12 characters")
	}
	if len(password) > 1024 {
		return "", errors.New("password too long")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("could not generate salt")
	}
	salt := make([]byte, 0, 22)
	var bits uint32
	var count uint
	for _, b := range random {
		bits |= uint32(b) << count
		count += 8
		for count >= 6 {
			salt = append(salt, cryptAlphabet[bits&63])
			bits >>= 6
			count -= 6
		}
	}
	if count > 0 {
		salt = append(salt, cryptAlphabet[bits&63])
	}
	hash, err := yescrypt.Hash([]byte(password), []byte("$y$j9T$"+string(salt)))
	if err != nil {
		return "", errors.New("could not generate password hash")
	}
	return string(hash), nil
}

func verifyFSPassword(encoded, password string) bool {
	if encoded == "" || password == "" {
		return false
	}
	if len(password) > 1024 {
		return false
	}
	hash, err := yescrypt.Hash([]byte(password), []byte(encoded))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(hash, []byte(encoded)) == 1
}

func (s Store) FileServerSettings(ctx context.Context) (FileServerSettings, error) {
	var cfg FileServerSettings
	err := s.Pool.QueryRow(ctx, "SELECT smb_enabled, sftp_enabled, sftp_port FROM file_server_settings WHERE id=1").
		Scan(&cfg.SMBEnabled, &cfg.SFTPEnabled, &cfg.SFTPPort)
	if errors.Is(err, pgx.ErrNoRows) {
		return FileServerSettings{
			SMBEnabled:  false,
			SFTPEnabled: false,
			SFTPPort:    DefaultSFTPPort,
		}, nil
	}
	if err != nil {
		return FileServerSettings{}, err
	}
	return cfg, nil
}

func (s Store) SetFileServerSettings(ctx context.Context, settings FileServerSettings) error {
	if err := ValidateFileServerSettings(settings); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO file_server_settings (id, smb_enabled, sftp_enabled, sftp_port, updated_at)
		VALUES (1, $1, $2, $3, now())
		ON CONFLICT (id) DO UPDATE SET
			smb_enabled = EXCLUDED.smb_enabled,
			sftp_enabled = EXCLUDED.sftp_enabled,
			sftp_port = EXCLUDED.sftp_port,
			updated_at = now()
	`, settings.SMBEnabled, settings.SFTPEnabled, settings.SFTPPort)
	return err
}

func (s Store) SetUserFileAccess(ctx context.Context, id int64, mode, password string) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("invalid user id")
	}
	if !ValidFSAccessMode(mode) {
		return "", errInvalidFSAccessMode
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var username string
	var enabled bool
	var curMode string
	var curHash string
	err = tx.QueryRow(ctx, "SELECT username, enabled, fs_access_mode, fs_password_hash FROM users WHERE id=$1 FOR UPDATE", id).
		Scan(&username, &enabled, &curMode, &curHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errUserNotFound
	}
	if err != nil {
		return "", err
	}

	if mode == FSAccessNone {
		_, err = tx.Exec(ctx, "UPDATE users SET fs_access_mode='none', fs_password_hash='', fs_password_changed_at=NULL WHERE id=$1", id)
		if err != nil {
			return "", err
		}
		return username, tx.Commit(ctx)
	}

	// mode is "ro" or "rw"
	if password == "" {
		if curHash == "" {
			return "", errFSPasswordRequired
		}
		_, err = tx.Exec(ctx, "UPDATE users SET fs_access_mode=$1 WHERE id=$2", mode, id)
		if err != nil {
			return "", err
		}
		return username, tx.Commit(ctx)
	}

	hash, err := hashFSPassword(password)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, "UPDATE users SET fs_access_mode=$1, fs_password_hash=$2, fs_password_changed_at=now() WHERE id=$3", mode, hash, id)
	if err != nil {
		return "", err
	}
	return username, tx.Commit(ctx)
}

func (s Store) SetOwnFilePassword(ctx context.Context, id int64, password string) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("invalid user id")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var username string
	var enabled bool
	var mode string
	err = tx.QueryRow(ctx, "SELECT username, enabled, fs_access_mode FROM users WHERE id=$1 FOR UPDATE", id).
		Scan(&username, &enabled, &mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", errUserNotFound
	}
	if err != nil {
		return "", err
	}
	if !enabled || (mode != FSAccessRO && mode != FSAccessRW) {
		return "", errFSNotEnabled
	}

	hash, err := hashFSPassword(password)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, "UPDATE users SET fs_password_hash=$1, fs_password_changed_at=now() WHERE id=$2", hash, id)
	if err != nil {
		return "", err
	}
	return username, tx.Commit(ctx)
}

func (s Store) AuthenticateFSUser(ctx context.Context, username, password string) (User, error) {
	if username == "" || password == "" {
		return User{}, fmt.Errorf("invalid credentials")
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, username, auth_source, coalesce(external_id,''), display_name, role, enabled, language, theme, password_changed_at, must_change_password, password_policy_mode, coalesce(password_max_age_days,0), fs_access_mode, fs_password_hash, fs_password_changed_at FROM users WHERE lower(username)=lower($1) ORDER BY id`, username)
	if err != nil {
		return User{}, err
	}
	defer rows.Close()
	var sawAny, sawEnabled, sawFS bool
	for rows.Next() {
		sawAny = true
		var u User
		var fsHash string
		if err := rows.Scan(&u.ID, &u.Username, &u.AuthSource, &u.ExternalID, &u.DisplayName, &u.Role, &u.Enabled, &u.Language, &u.Theme, &u.PasswordChangedAt, &u.MustChangePassword, &u.PasswordPolicyMode, &u.PasswordMaxAgeDays, &u.FSAccessMode, &fsHash, &u.FSPasswordChangedAt); err != nil {
			return User{}, err
		}
		if !u.Enabled {
			continue
		}
		sawEnabled = true
		if u.FSAccessMode != FSAccessRO && u.FSAccessMode != FSAccessRW {
			continue
		}
		sawFS = true
		if fsHash != "" && verifyFSPassword(fsHash, password) {
			u.HasFSPassword = true
			return u, nil
		}
	}
	if err := rows.Err(); err != nil {
		return User{}, err
	}
	if !sawAny {
		return User{}, fmt.Errorf("invalid credentials")
	}
	if !sawEnabled {
		return User{}, fmt.Errorf("user disabled")
	}
	if !sawFS {
		return User{}, errFSNotEnabled
	}
	return User{}, fmt.Errorf("invalid credentials")
}

func (s Store) FileServerUsers(ctx context.Context) ([]FileServerUser, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id, username, fs_access_mode, enabled, (fs_password_hash <> '') FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileServerUser{}
	for rows.Next() {
		var u FileServerUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Mode, &u.Enabled, &u.HasPassword); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
