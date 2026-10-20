package control

import (
	"context"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID       int64
	Username string
	Role     string
	Enabled  bool
	Language string
}
type AuditEvent struct {
	At     time.Time
	Actor  string
	Action string
	Result string
	Detail string
}
type MenuVersion struct {
	ID      int64
	At      time.Time
	Author  string
	Title   string
	Label   string
	Message string
	Script  string
	SHA256  string
	Active  bool
}
type Store struct{ Pool *pgxpool.Pool }

var userNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)

func (s Store) CreateInitialAdmin(ctx context.Context, username, password string) error {
	if !userNamePattern.MatchString(username) {
		return fmt.Errorf("invalid username")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(724311)"); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("initial admin already exists")
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users (username,password_hash,role) VALUES ($1,$2,'admin')", username, hash); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) Login(ctx context.Context, username, password string) (User, string, error) {
	var u User
	var hash string
	err := s.Pool.QueryRow(ctx, "SELECT id,username,role,enabled,password_hash FROM users WHERE lower(username)=lower($1)", username).Scan(&u.ID, &u.Username, &u.Role, &u.Enabled, &hash)
	if err != nil || !u.Enabled || !VerifyPassword(hash, password) {
		return User{}, "", fmt.Errorf("invalid credentials")
	}
	token, err := randomToken()
	if err != nil {
		return User{}, "", err
	}
	_, err = s.Pool.Exec(ctx, "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ($1,$2,$3)", tokenHash(token), u.ID, time.Now().Add(sessionLifetime))
	return u, token, err
}

func (s Store) Session(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, fmt.Errorf("missing session")
	}
	var u User
	var expiry time.Time
	err := s.Pool.QueryRow(ctx, "SELECT u.id,u.username,u.role,u.enabled,u.language,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1", tokenHash(token)).Scan(&u.ID, &u.Username, &u.Role, &u.Enabled, &u.Language, &expiry)
	if err != nil {
		return User{}, err
	}
	if !SessionValid(expiry, u.Enabled) {
		return User{}, fmt.Errorf("session expired or disabled")
	}
	return u, nil
}

func (s Store) Logout(ctx context.Context, token string) error {
	_, err := s.Pool.Exec(ctx, "DELETE FROM sessions WHERE token_hash=$1", tokenHash(token))
	return err
}

func (s Store) Audit(ctx context.Context, actorID int64, action, result, detail string) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO audit_events (actor_id,action,result,detail) VALUES ($1,$2,$3,$4)", actorID, action, result, detail)
	return err
}

func (s Store) AuditList(ctx context.Context) ([]AuditEvent, error) {
	rows, err := s.Pool.Query(ctx, "SELECT a.occurred_at,coalesce(u.username,'system'),a.action,a.result,a.detail FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var a AuditEvent
		if err := rows.Scan(&a.At, &a.Actor, &a.Action, &a.Result, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, "SELECT id,username,role,enabled FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.Enabled); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s Store) CreateUser(ctx context.Context, username, password, role string) error {
	if !userNamePattern.MatchString(username) || !Allowed(role, "view") {
		return fmt.Errorf("invalid username or role")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, "INSERT INTO users (username,password_hash,role) VALUES ($1,$2,$3)", username, hash, role)
	return err
}

func (s Store) ChangeUser(ctx context.Context, id, actorID int64, role string, enabled bool) error {
	if !Allowed(role, "view") {
		return fmt.Errorf("invalid role")
	}
	if id == actorID && (!enabled || role != "admin") {
		return fmt.Errorf("cannot remove your own administrator access")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(724312)"); err != nil {
		return err
	}
	var oldRole string
	var oldEnabled bool
	if err := tx.QueryRow(ctx, "SELECT role,enabled FROM users WHERE id=$1 FOR UPDATE", id).Scan(&oldRole, &oldEnabled); err != nil {
		return err
	}
	if oldRole == "admin" && oldEnabled && (role != "admin" || !enabled) {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND enabled").Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return fmt.Errorf("cannot disable the last administrator")
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE users SET role=$2,enabled=$3 WHERE id=$1", id, role, enabled); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) ResetPassword(ctx context.Context, id int64, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var updated int64
	if err := tx.QueryRow(ctx, "UPDATE users SET password_hash=$2 WHERE id=$1 RETURNING id", id, hash).Scan(&updated); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s Store) UserID(ctx context.Context, username string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, "SELECT id FROM users WHERE lower(username)=lower($1)", username).Scan(&id)
	return id, err
}

func (s Store) AuditSystem(ctx context.Context, action, result, detail string) error {
	_, err := s.Pool.Exec(ctx, "INSERT INTO audit_events (actor_id,action,result,detail) VALUES (NULL,$1,$2,$3)", action, result, detail)
	return err
}

func (s Store) MenuVersions(ctx context.Context) ([]MenuVersion, error) {
	rows, err := s.Pool.Query(ctx, "SELECT m.id,m.created_at,coalesce(u.username,'system'),m.title,m.diagnostic_label,m.diagnostic_message,m.script,m.sha256,m.active FROM menu_versions m LEFT JOIN users u ON u.id=m.author_id ORDER BY m.id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MenuVersion{}
	for rows.Next() {
		var m MenuVersion
		if err := rows.Scan(&m.ID, &m.At, &m.Author, &m.Title, &m.Label, &m.Message, &m.Script, &m.SHA256, &m.Active); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s Store) MenuByID(ctx context.Context, id int64) (MenuVersion, error) {
	var m MenuVersion
	err := s.Pool.QueryRow(ctx, "SELECT id,created_at,title,diagnostic_label,diagnostic_message,script,sha256,active FROM menu_versions WHERE id=$1", id).Scan(&m.ID, &m.At, &m.Title, &m.Label, &m.Message, &m.Script, &m.SHA256, &m.Active)
	return m, err
}

func (s Store) SaveActiveMenu(ctx context.Context, authorID int64, m MenuVersion) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "UPDATE menu_versions SET active=FALSE WHERE active"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO menu_versions (author_id,title,diagnostic_label,diagnostic_message,script,sha256,active) VALUES (NULLIF($1,0),$2,$3,$4,$5,$6,TRUE)", authorID, m.Title, m.Label, m.Message, m.Script, m.SHA256); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetUserLanguage stores the interface language chosen by a user.
func (s Store) SetUserLanguage(ctx context.Context, id int64, language string) error {
	if language != langRU && language != langEN {
		return fmt.Errorf("unsupported language")
	}
	command, err := s.Pool.Exec(ctx, "UPDATE users SET language=$2 WHERE id=$1", id, language)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ChangeOwnPassword replaces a user's password after checking the current one,
// and ends the user's other sessions while keeping the current one.
func (s Store) ChangeOwnPassword(ctx context.Context, id int64, sessionToken, current, next string) error {
	if utf8.RuneCountInString(next) < 12 {
		return errShortPassword
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hash string
	if err := tx.QueryRow(ctx, "SELECT password_hash FROM users WHERE id=$1 AND enabled FOR UPDATE", id).Scan(&hash); err != nil {
		return err
	}
	if !VerifyPassword(hash, current) {
		return errWrongPassword
	}
	newHash, err := HashPassword(next)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE users SET password_hash=$2 WHERE id=$1", id, newHash); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2", id, tokenHash(sessionToken)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
