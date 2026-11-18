package control

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"omb/internal/network"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID       int64
	Username string
	Role     string
	Enabled  bool
	Language string
	Theme    string
}
type AuditEvent struct {
	ID     int64
	At     time.Time
	Actor  string
	Action string
	Result string
	Detail string
}
type MenuVersion struct {
	ID                    int64
	At                    time.Time
	Author                string
	Title                 string
	Label                 string
	Message               string
	Script                string
	SHA256                string
	Active                bool
	NoDiagnostics         bool
	LocalTimeout          int
	NoLocalTimer          bool
	DefaultInstallationID int64
	LegacyUbuntuSourceID  int64
	Look                  string
	// Items is non-nil for structural versions, including empty menus.
	Items []MenuVersionItem
}

// MenuVersionItem is a saved menu placement, independent of the script.
type MenuVersionItem struct {
	VersionID      int64
	Components     *InstallationComponents
	InstallationID int64
	Position       int
	Title          string
	Folder         string
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
	err := s.Pool.QueryRow(ctx, "SELECT u.id,u.username,u.role,u.enabled,u.language,u.theme,s.expires_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=$1", tokenHash(token)).Scan(&u.ID, &u.Username, &u.Role, &u.Enabled, &u.Language, &u.Theme, &expiry)
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
	rows, err := s.Pool.Query(ctx, "SELECT a.id, a.occurred_at, coalesce(u.username,'system'), a.action, a.result, a.detail FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var a AuditEvent
		if err := rows.Scan(&a.ID, &a.At, &a.Actor, &a.Action, &a.Result, &a.Detail); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s Store) AuditPaged(ctx context.Context, limit int, beforeID int64) ([]AuditEvent, bool, error) {
	if limit < 1 || limit > 1000 {
		limit = 50
	}
	fetchLimit := limit + 1
	var query string
	var args []any
	if beforeID > 0 {
		query = "SELECT a.id, a.occurred_at, coalesce(u.username,'system'), a.action, a.result, a.detail FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id WHERE a.id < $1 ORDER BY a.id DESC LIMIT $2"
		args = []any{beforeID, fetchLimit}
	} else {
		query = "SELECT a.id, a.occurred_at, coalesce(u.username,'system'), a.action, a.result, a.detail FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT $1"
		args = []any{fetchLimit}
	}
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var a AuditEvent
		if err := rows.Scan(&a.ID, &a.At, &a.Actor, &a.Action, &a.Result, &a.Detail); err != nil {
			return nil, false, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
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

var (
	errSelfDemote   = errors.New("cannot remove your own administrator access")
	errSelfDelete   = errors.New("cannot delete your own account")
	errLastAdmin    = errors.New("cannot disable the last administrator")
	errUserNotFound = errors.New("user not found")
)

func (s Store) DeleteUser(ctx context.Context, id, actorID int64) (string, error) {
	if id <= 0 {
		return "", fmt.Errorf("invalid user id")
	}
	if id == actorID {
		return "", errSelfDelete
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(724312)"); err != nil {
		return "", err
	}
	var username, role string
	var enabled bool
	if err := tx.QueryRow(ctx, "SELECT username,role,enabled FROM users WHERE id=$1 FOR UPDATE", id).Scan(&username, &role, &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errUserNotFound
		}
		return "", err
	}
	if role == "admin" && enabled {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='admin' AND enabled").Scan(&count); err != nil {
			return "", err
		}
		if count <= 1 {
			return "", errLastAdmin
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM users WHERE id=$1", id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return username, nil
}

func (s Store) ChangeUser(ctx context.Context, id, actorID int64, role string, enabled bool) error {
	if !Allowed(role, "view") {
		return fmt.Errorf("invalid role")
	}
	if id == actorID && (!enabled || role != "admin") {
		return errSelfDemote
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
			return errLastAdmin
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

// menuVersionSelect reads metadata and placements in a single snapshot.
const menuVersionSelect = `SELECT m.id,m.created_at,coalesce(u.username,'system'),m.title,m.diagnostic_label,m.diagnostic_message,m.script,m.sha256,m.active,
 NOT m.diagnostics_on,m.local_timeout,NOT m.local_timer,m.look,m.default_installation_id,COALESCE(m.legacy_ubuntu_source_id,0),
 i.installation_id,i.position,i.title,i.folder,i.components FROM `

func (s Store) MenuVersions(ctx context.Context) ([]MenuVersion, error) {
	rows, err := s.Pool.Query(ctx, menuVersionSelect+`(SELECT * FROM menu_versions ORDER BY id DESC LIMIT 50) m
 LEFT JOIN users u ON u.id=m.author_id LEFT JOIN menu_version_items i ON i.version_id=m.id ORDER BY m.id DESC,i.position`)
	if err != nil {
		return nil, err
	}
	return readMenuVersions(rows)
}

func (s Store) MenuByID(ctx context.Context, id int64) (MenuVersion, error) {
	rows, err := s.Pool.Query(ctx, menuVersionSelect+`menu_versions m LEFT JOIN users u ON u.id=m.author_id
 LEFT JOIN menu_version_items i ON i.version_id=m.id WHERE m.id=$1 ORDER BY i.position`, id)
	if err != nil {
		return MenuVersion{}, err
	}
	versions, err := readMenuVersions(rows)
	if err != nil {
		return MenuVersion{}, err
	}
	if len(versions) == 0 {
		return MenuVersion{}, pgx.ErrNoRows
	}
	return versions[0], nil
}

func readMenuVersions(rows pgx.Rows) ([]MenuVersion, error) {
	defer rows.Close()
	versions := []MenuVersion{}
	for rows.Next() {
		var m MenuVersion
		var installationID *int64
		var position *int
		var title, folder *string
		var components *InstallationComponents
		if err := rows.Scan(&m.ID, &m.At, &m.Author, &m.Title, &m.Label, &m.Message, &m.Script, &m.SHA256, &m.Active,
			&m.NoDiagnostics, &m.LocalTimeout, &m.NoLocalTimer, &m.Look, &m.DefaultInstallationID, &m.LegacyUbuntuSourceID, &installationID, &position, &title, &folder, &components); err != nil {
			return nil, err
		}
		if len(versions) == 0 || versions[len(versions)-1].ID != m.ID {
			m.Items = make([]MenuVersionItem, 0)
			versions = append(versions, m)
		}
		if installationID != nil {
			last := &versions[len(versions)-1]
			last.Items = append(last.Items, MenuVersionItem{VersionID: m.ID, InstallationID: *installationID, Position: *position, Title: *title, Folder: *folder, Components: components})
		}
	}
	return versions, rows.Err()
}

// SaveActiveMenu commits the new version, its settings and all placements together.
func (s Store) SaveActiveMenu(ctx context.Context, authorID int64, m MenuVersion) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "UPDATE menu_versions SET active=FALSE WHERE active"); err != nil {
		return err
	}
	timeout := m.LocalTimeout
	if timeout <= 0 {
		timeout = defaultLocalTimeout
	}
	var versionID int64
	if err := tx.QueryRow(ctx, `INSERT INTO menu_versions (author_id,title,diagnostic_label,diagnostic_message,script,sha256,active,diagnostics_on,local_timeout,local_timer,look,default_installation_id,legacy_ubuntu_source_id)
 VALUES (NULLIF($1,0),$2,$3,$4,$5,$6,TRUE,$7,$8,$9,$10,$11,NULLIF($12,0)) RETURNING id`, authorID, m.Title, m.Label, m.Message, m.Script, m.SHA256, !m.NoDiagnostics, timeout, !m.NoLocalTimer, m.Look, m.DefaultInstallationID, m.LegacyUbuntuSourceID).Scan(&versionID); err != nil {
		return err
	}
	for _, item := range m.Items {
		// Historical references survive entry deletion. New publications still
		// require live entries and lock them against concurrent deletion.
		var id int64
		if err := tx.QueryRow(ctx, `SELECT id FROM installations WHERE id=$1 FOR KEY SHARE`, item.InstallationID).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO menu_version_items (version_id,installation_id,position,title,folder,components) VALUES ($1,$2,$3,$4,$5,$6)`, versionID, item.InstallationID, item.Position, item.Title, item.Folder, item.Components); err != nil {
			return err
		}
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

// SetUserTheme stores the color theme chosen by a user.
func (s Store) SetUserTheme(ctx context.Context, id int64, theme string) error {
	if !validTheme(theme) {
		return fmt.Errorf("unsupported theme")
	}
	command, err := s.Pool.Exec(ctx, "UPDATE users SET theme=$2 WHERE id=$1", id, theme)
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

// Machines returns the book of machines, most recently seen first.
func (s Store) Machines(ctx context.Context) ([]MachineInfo, error) {
	rows, err := s.Pool.Query(ctx, `SELECT mac,first_seen,last_seen,last_ip,firmware,manufacturer,product,serial,uuid,detected_at,name,manufacturer_override,product_override,note,manual,network_driver FROM machines ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MachineInfo
	for rows.Next() {
		var m MachineInfo
		if err := rows.Scan(&m.MAC, &m.FirstSeen, &m.LastSeen, &m.LastIP, &m.Firmware, &m.Manufacturer, &m.Product, &m.Serial, &m.UUID, &m.DetectedAt, &m.Name, &m.ManufacturerOverride, &m.ProductOverride, &m.Note, &m.Manual, &m.NetworkDriver); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SeeMachines adds machines to the book and moves their last-seen data on.
// SMBIOS details change only with a newer hello report; times never go back.
func (s Store) SeeMachines(ctx context.Context, seen []MachineSeen) error {
	batch := &pgx.Batch{}
	for _, m := range seen {
		var detected *time.Time
		if !m.DetectedAt.IsZero() {
			at := m.DetectedAt
			detected = &at
		}
		batch.Queue(`INSERT INTO machines (mac,first_seen,last_seen,last_ip,firmware,manufacturer,product,serial,uuid,detected_at)
VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (mac) DO UPDATE SET
 manual=false,
 first_seen=LEAST(machines.first_seen,EXCLUDED.first_seen),
 last_seen=GREATEST(machines.last_seen,EXCLUDED.last_seen),
 last_ip=CASE WHEN EXCLUDED.last_seen>=machines.last_seen AND EXCLUDED.last_ip<>'' THEN EXCLUDED.last_ip ELSE machines.last_ip END,
 firmware=CASE WHEN EXCLUDED.last_seen>=machines.last_seen AND EXCLUDED.firmware<>'' THEN EXCLUDED.firmware ELSE machines.firmware END,
 manufacturer=CASE WHEN EXCLUDED.detected_at>COALESCE(machines.detected_at,'-infinity') THEN EXCLUDED.manufacturer ELSE machines.manufacturer END,
 product=CASE WHEN EXCLUDED.detected_at>COALESCE(machines.detected_at,'-infinity') THEN EXCLUDED.product ELSE machines.product END,
 serial=CASE WHEN EXCLUDED.detected_at>COALESCE(machines.detected_at,'-infinity') THEN EXCLUDED.serial ELSE machines.serial END,
 uuid=CASE WHEN EXCLUDED.detected_at>COALESCE(machines.detected_at,'-infinity') THEN EXCLUDED.uuid ELSE machines.uuid END,
 detected_at=GREATEST(machines.detected_at,EXCLUDED.detected_at)`,
			m.MAC, m.At, m.IP, m.Firmware, m.Manufacturer, m.Product, m.Serial, m.UUID, detected)
	}
	results := s.Pool.SendBatch(ctx, batch)
	defer results.Close()
	for range seen {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// EditMachine stores a machine's name, note and maker or model override; a
// machine not yet in the book is added.
func (s Store) EditMachine(ctx context.Context, mac string, edit MachineEdit) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO machines (mac,first_seen,last_seen,name,note,manufacturer_override,product_override,updated_at)
VALUES ($1,now(),now(),$2,$3,$4,$5,now())
ON CONFLICT (mac) DO UPDATE SET name=$2,note=$3,manufacturer_override=$4,product_override=$5,updated_at=now()`,
		mac, edit.Name, edit.Note, edit.Manufacturer, edit.Product)
	return err
}

// AddMachine adds a machine a person typed in by MAC. It is marked manual
// until it boots; an existing MAC is left alone.
func (s Store) AddMachine(ctx context.Context, mac string, edit MachineEdit) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `INSERT INTO machines (mac,first_seen,last_seen,name,note,manufacturer_override,product_override,manual,updated_at)
VALUES ($1,now(),now(),$2,$3,$4,$5,true,now())
ON CONFLICT (mac) DO NOTHING`,
		mac, edit.Name, edit.Note, edit.Manufacturer, edit.Product)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ApplyMachineImport adds and changes machines from a loaded file in one
// transaction: all of it is written, or nothing.
func (s Store) ApplyMachineImport(ctx context.Context, items []machineImportChange) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, it := range items {
		e := it.Edit
		if it.Add {
			_, err = tx.Exec(ctx, `INSERT INTO machines (mac,first_seen,last_seen,name,note,manufacturer_override,product_override,manual,updated_at)
VALUES ($1,now(),now(),$2,$3,$4,$5,true,now()) ON CONFLICT (mac) DO NOTHING`, it.MAC, e.Name, e.Note, e.Manufacturer, e.Product)
		} else {
			_, err = tx.Exec(ctx, `UPDATE machines SET name=$2,note=$3,manufacturer_override=$4,product_override=$5,updated_at=now() WHERE mac=$1`, it.MAC, e.Name, e.Note, e.Manufacturer, e.Product)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// MacRules returns the MAC prefix rules.
func (s Store) MacRules(ctx context.Context) ([]MacRule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT prefix,title,manufacturer,note,network_driver FROM mac_prefix_rules ORDER BY prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MacRule
	for rows.Next() {
		var r MacRule
		if err := rows.Scan(&r.Prefix, &r.Title, &r.Manufacturer, &r.Note, &r.NetworkDriver); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddMacRule adds a rule; false when the prefix already has one.
func (s Store) AddMacRule(ctx context.Context, r MacRule) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `INSERT INTO mac_prefix_rules (prefix,title,manufacturer,note,network_driver) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (prefix) DO NOTHING`, r.Prefix, r.Title, r.Manufacturer, r.Note, r.NetworkDriver)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// EditMacRule changes a rule's title, maker and note.
func (s Store) EditMacRule(ctx context.Context, r MacRule) error {
	_, err := s.Pool.Exec(ctx, `UPDATE mac_prefix_rules SET title=$2,manufacturer=$3,note=$4,network_driver=$5,updated_at=now() WHERE prefix=$1`, r.Prefix, r.Title, r.Manufacturer, r.Note, r.NetworkDriver)
	return err
}

// DeleteMacRule removes a rule.
func (s Store) DeleteMacRule(ctx context.Context, prefix string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM mac_prefix_rules WHERE prefix=$1`, prefix)
	return err
}

// DeleteMachine removes a machine from the book; if it boots again it comes
// back without its name.
func (s Store) DeleteMachine(ctx context.Context, mac string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM machines WHERE mac=$1`, mac)
	return err
}

func (s Store) SetMachineNetworkDriver(ctx context.Context, mac, driver string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE machines SET network_driver=$2,updated_at=now() WHERE mac=$1`, mac, driver)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("machine not found")
	}
	return nil
}

// BootDriverRules reads one consistent snapshot of all explicit selections.
func (s Store) BootDriverRules(ctx context.Context) ([]network.BootDriverRule, error) {
	rows, err := s.Pool.Query(ctx, `SELECT mac,network_driver FROM machines WHERE network_driver<>'' UNION ALL SELECT prefix,network_driver FROM mac_prefix_rules WHERE network_driver<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []network.BootDriverRule{}
	for rows.Next() {
		var rule network.BootDriverRule
		if err := rows.Scan(&rule.Match, &rule.Driver); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}
