package ldapclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	startTLSOID = "1.3.6.1.4.1.1466.20037"

	ScopeBaseObject   = 0
	ScopeSingleLevel  = 1
	ScopeWholeSubtree = 2
)

// DialOptions configures a strict TLS-only LDAPv3 connection.
type DialOptions struct {
	ServerURL string
	TLSMode   string // "ldaps" or "starttls"
	CAPEM     string
	Timeout   time.Duration
}

// Entry is a single LDAP SearchResultEntry.
type Entry struct {
	DN    string
	Attrs map[string][][]byte
}

// FirstString returns the first UTF-8 string value for attr (case-insensitive).
func (e Entry) FirstString(attr string) string {
	raw := e.FirstBytes(attr)
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

// FirstBytes returns the first raw byte slice for attr (case-insensitive).
func (e Entry) FirstBytes(attr string) []byte {
	vals := e.Values(attr)
	if len(vals) == 0 {
		return nil
	}
	return vals[0]
}

// Values returns all raw values for attr (case-insensitive).
func (e Entry) Values(attr string) [][]byte {
	return e.Attrs[strings.ToLower(attr)]
}

// Client is a minimal LDAPv3 client using only the Go standard library.
type Client struct {
	conn    net.Conn
	timeout time.Duration
	msgID   atomic.Int64
}

// Dial opens an LDAPS or StartTLS connection with strict TLS certificate verification.
func Dial(ctx context.Context, opts DialOptions) (*Client, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	host, port, scheme, err := parseServerURL(opts.ServerURL, opts.TLSMode)
	if err != nil {
		return nil, err
	}
	tlsCfg, err := buildTLSConfig(host, opts.CAPEM)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(host, port)

	if scheme == "ldaps" {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: tlsCfg}
		conn, err := tlsDialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("ldaps dial %s: %w", addr, err)
		}
		return &Client{conn: conn, timeout: timeout}, nil
	}

	// StartTLS over TCP (never falls back to plaintext).
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ldap dial %s: %w", addr, err)
	}
	c := &Client{conn: rawConn, timeout: timeout}
	if err := c.startTLS(ctx, tlsCfg); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return c, nil
}

func parseServerURL(rawURL, tlsMode string) (host, port, mode string, err error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", "", errors.New("LDAP server URL is required")
	}
	mode = strings.ToLower(strings.TrimSpace(tlsMode))
	if !strings.Contains(rawURL, "://") {
		if mode == "starttls" {
			rawURL = "ldap://" + rawURL
		} else {
			rawURL = "ldaps://" + rawURL
		}
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid LDAP URL: %w", err)
	}
	host = u.Hostname()
	port = u.Port()
	if host == "" {
		return "", "", "", errors.New("LDAP server host is required")
	}
	switch strings.ToLower(u.Scheme) {
	case "ldaps":
		if mode == "" {
			mode = "ldaps"
		}
		if mode != "ldaps" {
			return "", "", "", errors.New("ldaps:// URL requires TLS mode ldaps")
		}
		if port == "" {
			port = "636"
		}
	case "ldap":
		if mode == "" {
			mode = "starttls"
		}
		if mode != "starttls" {
			return "", "", "", errors.New("ldap:// URL requires TLS mode starttls (plaintext is forbidden)")
		}
		if port == "" {
			port = "389"
		}
	default:
		return "", "", "", fmt.Errorf("unsupported LDAP scheme %q", u.Scheme)
	}
	return host, port, mode, nil
}

func buildTLSConfig(serverName, caPEM string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}
	if strings.TrimSpace(caPEM) != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(caPEM)) {
			return nil, errors.New("invalid CA certificate PEM")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

func (c *Client) setDeadline(ctx context.Context) {
	dl := time.Now().Add(c.timeout)
	if ctxDL, ok := ctx.Deadline(); ok && ctxDL.Before(dl) {
		dl = ctxDL
	}
	_ = c.conn.SetDeadline(dl)
}

func (c *Client) nextID() int64 {
	return c.msgID.Add(1)
}

func (c *Client) startTLS(ctx context.Context, tlsCfg *tls.Config) error {
	id := c.nextID()
	req := encodeSequence(tagSequence,
		encodeInt(tagInteger, id),
		encodeSequence(appExtendedRequest, encodeTag(0x80, []byte(startTLSOID))),
	)
	c.setDeadline(ctx)
	if _, err := c.conn.Write(req); err != nil {
		return fmt.Errorf("starttls write: %w", err)
	}
	resp, err := readBERPacket(c.conn)
	if err != nil {
		return fmt.Errorf("starttls read: %w", err)
	}
	if err := checkLDAPResult(resp, id, appExtendedResponse); err != nil {
		return fmt.Errorf("starttls rejected: %w", err)
	}
	tlsConn := tls.Client(c.conn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return fmt.Errorf("starttls handshake: %w", err)
	}
	c.conn = tlsConn
	return nil
}

// Bind performs LDAPv3 Simple Bind. Empty passwords are always rejected.
func (c *Client) Bind(ctx context.Context, dn, password string) error {
	if strings.TrimSpace(dn) == "" || password == "" {
		return errors.New("bind DN and password are required")
	}
	id := c.nextID()
	req := encodeSequence(tagSequence,
		encodeInt(tagInteger, id),
		encodeSequence(appBindRequest,
			encodeInt(tagInteger, 3), // LDAPv3
			encodeString(dn),
			encodeTag(0x80, []byte(password)), // Simple auth
		),
	)
	c.setDeadline(ctx)
	if _, err := c.conn.Write(req); err != nil {
		return fmt.Errorf("ldap bind write: %w", err)
	}
	resp, err := readBERPacket(c.conn)
	if err != nil {
		return fmt.Errorf("ldap bind read: %w", err)
	}
	return checkLDAPResult(resp, id, appBindResponse)
}

// Search executes a subtree search and returns matching entries.
func (c *Client) Search(ctx context.Context, baseDN string, scope int, filter string, attrs []string, sizeLimit int) ([]Entry, error) {
	compiledFilter, err := CompileFilter(filter)
	if err != nil {
		return nil, err
	}
	attrBytes := make([][]byte, 0, len(attrs))
	for _, a := range attrs {
		attrBytes = append(attrBytes, encodeString(a))
	}
	id := c.nextID()
	req := encodeSequence(tagSequence,
		encodeInt(tagInteger, id),
		encodeSequence(appSearchRequest,
			encodeString(baseDN),
			encodeInt(tagEnumerated, int64(scope)),
			encodeInt(tagEnumerated, 0), // neverDerefAliases
			encodeInt(tagInteger, int64(sizeLimit)),
			encodeInt(tagInteger, int64(c.timeout.Seconds())),
			encodeBool(false), // typesOnly
			compiledFilter,
			encodeSequence(tagSequence, attrBytes...),
		),
	)
	c.setDeadline(ctx)
	if _, err := c.conn.Write(req); err != nil {
		return nil, fmt.Errorf("ldap search write: %w", err)
	}

	var entries []Entry
	for {
		c.setDeadline(ctx)
		pkt, err := readBERPacket(c.conn)
		if err != nil {
			return nil, fmt.Errorf("ldap search read: %w", err)
		}
		if len(pkt.Children) < 2 {
			return nil, errors.New("malformed LDAP response")
		}
		gotID, err := pkt.Children[0].intVal()
		if err != nil || gotID != id {
			continue
		}
		op := pkt.Children[1]
		switch op.Tag {
		case appSearchResultEntry:
			entry, err := parseSearchEntry(op)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		case appSearchResultRef:
			// Ignore referrals in AD subtree searches.
			continue
		case appSearchResultDone:
			if err := checkResultNode(op); err != nil {
				return nil, err
			}
			return entries, nil
		default:
			return nil, fmt.Errorf("unexpected LDAP tag 0x%02x", op.Tag)
		}
	}
}

// Close sends UnbindRequest and closes the underlying connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	id := c.nextID()
	req := encodeSequence(tagSequence,
		encodeInt(tagInteger, id),
		encodeTag(appUnbindRequest, nil),
	)
	_ = c.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_, _ = c.conn.Write(req)
	return c.conn.Close()
}

func checkLDAPResult(pkt *berNode, wantID int64, wantTag byte) error {
	if pkt.Tag != tagSequence || len(pkt.Children) < 2 {
		return errors.New("malformed LDAP message")
	}
	gotID, err := pkt.Children[0].intVal()
	if err != nil || gotID != wantID {
		return fmt.Errorf("LDAP message ID mismatch: got %d, want %d", gotID, wantID)
	}
	op := pkt.Children[1]
	if op.Tag != wantTag {
		return fmt.Errorf("unexpected LDAP response tag 0x%02x (want 0x%02x)", op.Tag, wantTag)
	}
	return checkResultNode(op)
}

func checkResultNode(op *berNode) error {
	if len(op.Children) < 3 {
		return errors.New("malformed LDAPResult")
	}
	code, err := op.Children[0].intVal()
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	diag := string(op.Children[2].Data)
	if diag != "" {
		return fmt.Errorf("LDAP error %d: %s", code, diag)
	}
	return fmt.Errorf("LDAP error %d", code)
}

func parseSearchEntry(op *berNode) (Entry, error) {
	if len(op.Children) < 2 {
		return Entry{}, errors.New("malformed SearchResultEntry")
	}
	entry := Entry{
		DN:    string(op.Children[0].Data),
		Attrs: map[string][][]byte{},
	}
	attrsSeq := op.Children[1]
	for _, attrNode := range attrsSeq.Children {
		if len(attrNode.Children) < 2 {
			continue
		}
		name := strings.ToLower(string(attrNode.Children[0].Data))
		valsNode := attrNode.Children[1]
		var vals [][]byte
		for _, v := range valsNode.Children {
			cp := make([]byte, len(v.Data))
			copy(cp, v.Data)
			vals = append(vals, cp)
		}
		entry.Attrs[name] = vals
	}
	return entry, nil
}
