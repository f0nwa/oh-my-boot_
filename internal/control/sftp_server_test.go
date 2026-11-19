package control

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// SFTPv3 protocol constants for tests
const (
	testFXPInit     = 1
	testFXPVersion  = 2
	testFXPOpen     = 3
	testFXPClose    = 4
	testFXPRead     = 5
	testFXPWrite    = 6
	testFXPLstat    = 7
	testFXPFstat    = 8
	testFXPSetstat  = 9
	testFXPFsetstat = 10
	testFXPOpendir  = 11
	testFXPReaddir  = 12
	testFXPRemove   = 13
	testFXPMkdir    = 14
	testFXPRmdir    = 15
	testFXPRealpath = 16
	testFXPStat     = 17
	testFXPRename   = 18

	testFXPStatus = 101
	testFXPHandle = 102
	testFXPData   = 103
	testFXPName   = 104
	testFXPAttrs  = 105

	testFXOK               = 0
	testFXEOF              = 1
	testFXNoSuchFile       = 2
	testFXPermissionDenied = 3
	testFXFailure          = 4
	testFXBadMessage       = 5
	testFXNoConnection     = 6
	testFXConnectionLost   = 7
	testFXOpUnsupported    = 8

	testFXFRead   = 0x00000001
	testFXFWrite  = 0x00000002
	testFXFAppend = 0x00000004
	testFXFCreat  = 0x00000008
	testFXFTrunc  = 0x00000010
	testFXFExcl   = 0x00000020
)

type sftpStatusError struct {
	Code uint32
	Msg  string
}

func (e *sftpStatusError) Error() string {
	return fmt.Sprintf("sftp status error: code=%d msg=%s", e.Code, e.Msg)
}

type testSFTPClient struct {
	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	reqID   uint32
	mu      sync.Mutex
}

func dialTestSFTP(t *testing.T, addr, username, password string) (*testSFTPClient, error) {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	if err := session.RequestSubsystem("sftp"); err != nil {
		session.Close()
		client.Close()
		return nil, err
	}

	tc := &testSFTPClient{
		client:  client,
		session: session,
		stdin:   stdin,
		stdout:  stdout,
	}

	if err := tc.init(); err != nil {
		tc.Close()
		return nil, err
	}
	return tc, nil
}

func (c *testSFTPClient) Close() error {
	var errs []error
	if c.session != nil {
		if err := c.session.Close(); err != nil && !errors.Is(err, io.EOF) {
			errs = append(errs, err)
		}
	}
	if c.client != nil {
		if err := c.client.Close(); err != nil && !errors.Is(err, io.EOF) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (c *testSFTPClient) nextID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqID++
	return c.reqID
}

func (c *testSFTPClient) sendPacket(typ byte, payload []byte) error {
	length := uint32(1 + len(payload))
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[0:4], length)
	hdr[4] = typ
	if _, err := c.stdin.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.stdin.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (c *testSFTPClient) recvPacket() (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(c.stdout, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf[:])
	if length == 0 {
		return 0, nil, errors.New("zero length packet")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(c.stdout, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func (c *testSFTPClient) init() error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], 3)
	if err := c.sendPacket(testFXPInit, buf[:]); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPVersion {
		return fmt.Errorf("expected FXP_VERSION (2), got %d", typ)
	}
	if len(payload) < 4 {
		return errors.New("short FXP_VERSION packet")
	}
	ver := binary.BigEndian.Uint32(payload[:4])
	if ver != 3 {
		return fmt.Errorf("expected version 3, got %d", ver)
	}
	return nil
}

func sftpPutString(buf *bytes.Buffer, s string) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	buf.Write(lenBuf[:])
	buf.WriteString(s)
}

func sftpPutUint32(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func sftpPutUint64(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

func sftpGetString(data []byte) (string, []byte, error) {
	if len(data) < 4 {
		return "", nil, errors.New("short string len")
	}
	l := binary.BigEndian.Uint32(data[:4])
	data = data[4:]
	if uint32(len(data)) < l {
		return "", nil, errors.New("short string data")
	}
	return string(data[:l]), data[l:], nil
}

func sftpGetUint32(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, errors.New("short uint32")
	}
	return binary.BigEndian.Uint32(data[:4]), data[4:], nil
}

func parseStatusPacket(payload []byte) error {
	if len(payload) < 8 {
		return errors.New("short status packet")
	}
	code := binary.BigEndian.Uint32(payload[4:8])
	msg, _, _ := sftpGetString(payload[8:])
	if code == testFXOK {
		return nil
	}
	return &sftpStatusError{Code: code, Msg: msg}
}

func (c *testSFTPClient) Open(path string, flags uint32) (string, error) {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	sftpPutUint32(&buf, flags)
	sftpPutUint32(&buf, 0) // attrs flags = 0
	if err := c.sendPacket(testFXPOpen, buf.Bytes()); err != nil {
		return "", err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return "", err
	}
	if typ == testFXPHandle {
		if len(payload) < 8 {
			return "", errors.New("short handle packet")
		}
		handle, _, err := sftpGetString(payload[4:])
		return handle, err
	}
	if typ == testFXPStatus {
		return "", parseStatusPacket(payload)
	}
	return "", fmt.Errorf("unexpected packet type %d", typ)
}

func (c *testSFTPClient) CloseHandle(handle string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, handle)
	if err := c.sendPacket(testFXPClose, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPStatus {
		return fmt.Errorf("expected FXP_STATUS, got %d", typ)
	}
	return parseStatusPacket(payload)
}

func (c *testSFTPClient) Write(handle string, offset uint64, data []byte) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, handle)
	sftpPutUint64(&buf, offset)
	sftpPutString(&buf, string(data))
	if err := c.sendPacket(testFXPWrite, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPStatus {
		return fmt.Errorf("expected FXP_STATUS, got %d", typ)
	}
	return parseStatusPacket(payload)
}

func (c *testSFTPClient) Read(handle string, offset uint64, length uint32) ([]byte, error) {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, handle)
	sftpPutUint64(&buf, offset)
	sftpPutUint32(&buf, length)
	if err := c.sendPacket(testFXPRead, buf.Bytes()); err != nil {
		return nil, err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return nil, err
	}
	if typ == testFXPData {
		if len(payload) < 8 {
			return nil, errors.New("short data packet")
		}
		data, _, err := sftpGetString(payload[4:])
		return []byte(data), err
	}
	if typ == testFXPStatus {
		return nil, parseStatusPacket(payload)
	}
	return nil, fmt.Errorf("unexpected packet type %d", typ)
}

func (c *testSFTPClient) Opendir(path string) (string, error) {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	if err := c.sendPacket(testFXPOpendir, buf.Bytes()); err != nil {
		return "", err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return "", err
	}
	if typ == testFXPHandle {
		if len(payload) < 8 {
			return "", errors.New("short handle packet")
		}
		handle, _, err := sftpGetString(payload[4:])
		return handle, err
	}
	if typ == testFXPStatus {
		return "", parseStatusPacket(payload)
	}
	return "", fmt.Errorf("unexpected packet type %d", typ)
}

func (c *testSFTPClient) Readdir(handle string) ([]string, error) {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, handle)
	if err := c.sendPacket(testFXPReaddir, buf.Bytes()); err != nil {
		return nil, err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return nil, err
	}
	if typ == testFXPStatus {
		return nil, parseStatusPacket(payload)
	}
	if typ != testFXPName {
		return nil, fmt.Errorf("expected FXP_NAME, got %d", typ)
	}
	if len(payload) < 8 {
		return nil, errors.New("short name packet")
	}
	count := binary.BigEndian.Uint32(payload[4:8])
	rem := payload[8:]
	var names []string
	for i := uint32(0); i < count; i++ {
		filename, rest, err := sftpGetString(rem)
		if err != nil {
			return nil, err
		}
		names = append(names, filename)
		_, rest, err = sftpGetString(rest) // skip longname
		if err != nil {
			return nil, err
		}
		// skip attrs
		if len(rest) < 4 {
			return nil, errors.New("short attrs flags")
		}
		aflags := binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		if aflags&0x00000001 != 0 { // size
			if len(rest) < 8 {
				return nil, errors.New("short attrs size")
			}
			rest = rest[8:]
		}
		if aflags&0x00000002 != 0 { // uid/gid
			if len(rest) < 8 {
				return nil, errors.New("short attrs uidgid")
			}
			rest = rest[8:]
		}
		if aflags&0x00000004 != 0 { // permissions
			if len(rest) < 4 {
				return nil, errors.New("short attrs permissions")
			}
			rest = rest[4:]
		}
		if aflags&0x00000008 != 0 { // acmodtime
			if len(rest) < 8 {
				return nil, errors.New("short attrs acmodtime")
			}
			rest = rest[8:]
		}
		if aflags&0x80000000 != 0 { // extended
			if len(rest) < 4 {
				return nil, errors.New("short attrs extended count")
			}
			extCount := binary.BigEndian.Uint32(rest[:4])
			rest = rest[4:]
			for e := uint32(0); e < extCount; e++ {
				_, rest, err = sftpGetString(rest)
				if err != nil {
					return nil, err
				}
				_, rest, err = sftpGetString(rest)
				if err != nil {
					return nil, err
				}
			}
		}
		rem = rest
	}
	return names, nil
}

func (c *testSFTPClient) Stat(path string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	if err := c.sendPacket(testFXPStat, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ == testFXPAttrs {
		return nil
	}
	if typ == testFXPStatus {
		return parseStatusPacket(payload)
	}
	return fmt.Errorf("unexpected packet type %d", typ)
}

func (c *testSFTPClient) Lstat(path string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	if err := c.sendPacket(testFXPLstat, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ == testFXPAttrs {
		return nil
	}
	if typ == testFXPStatus {
		return parseStatusPacket(payload)
	}
	return fmt.Errorf("unexpected packet type %d", typ)
}

func (c *testSFTPClient) Remove(path string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	if err := c.sendPacket(testFXPRemove, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPStatus {
		return fmt.Errorf("expected FXP_STATUS, got %d", typ)
	}
	return parseStatusPacket(payload)
}

func (c *testSFTPClient) Rename(oldPath, newPath string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, oldPath)
	sftpPutString(&buf, newPath)
	if err := c.sendPacket(testFXPRename, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPStatus {
		return fmt.Errorf("expected FXP_STATUS, got %d", typ)
	}
	return parseStatusPacket(payload)
}

func (c *testSFTPClient) Mkdir(path string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	sftpPutUint32(&buf, 0) // attrs
	if err := c.sendPacket(testFXPMkdir, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPStatus {
		return fmt.Errorf("expected FXP_STATUS, got %d", typ)
	}
	return parseStatusPacket(payload)
}

func (c *testSFTPClient) Rmdir(path string) error {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	if err := c.sendPacket(testFXPRmdir, buf.Bytes()); err != nil {
		return err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return err
	}
	if typ != testFXPStatus {
		return fmt.Errorf("expected FXP_STATUS, got %d", typ)
	}
	return parseStatusPacket(payload)
}

func (c *testSFTPClient) Realpath(path string) (string, error) {
	id := c.nextID()
	var buf bytes.Buffer
	sftpPutUint32(&buf, id)
	sftpPutString(&buf, path)
	if err := c.sendPacket(testFXPRealpath, buf.Bytes()); err != nil {
		return "", err
	}
	typ, payload, err := c.recvPacket()
	if err != nil {
		return "", err
	}
	if typ == testFXPStatus {
		return "", parseStatusPacket(payload)
	}
	if typ != testFXPName {
		return "", fmt.Errorf("expected FXP_NAME, got %d", typ)
	}
	if len(payload) < 8 {
		return "", errors.New("short name packet")
	}
	filename, _, err := sftpGetString(payload[8:])
	return filename, err
}

// Tests start here

func TestLocalSFTPAuthAndShellRejection(t *testing.T) {
	rootDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "host_ed25519")

	users := map[string]struct {
		user User
		pass string
	}{
		"alice": {
			user: User{ID: 1, Username: "alice", Enabled: true, FSAccessMode: "rw"},
			pass: "secret-rw-pass12",
		},
		"bob": {
			user: User{ID: 2, Username: "bob", Enabled: true, FSAccessMode: "ro"},
			pass: "secret-ro-pass12",
		},
		"disabled": {
			user: User{ID: 3, Username: "disabled", Enabled: false, FSAccessMode: "rw"},
			pass: "secret-dis-pass12",
		},
		"none": {
			user: User{ID: 4, Username: "none", Enabled: true, FSAccessMode: "none"},
			pass: "secret-none-pass12",
		},
	}

	hooks := SFTPHooks{
		Authenticate: func(ctx context.Context, username, password string) (User, error) {
			u, ok := users[username]
			if !ok || u.pass != password {
				return User{}, errors.New("invalid credentials")
			}
			return u.user, nil
		},
	}

	server := NewLocalSFTPServer(rootDir, keyPath, hooks)
	defer server.Close()

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("server.Apply failed: %v", err)
	}

	running, addr := server.Running()
	if !running || addr == "" {
		t.Fatalf("expected server running, got running=%v addr=%s", running, addr)
	}

	// 1. Valid rw auth succeeds
	clientRW, err := dialTestSFTP(t, addr, "alice", "secret-rw-pass12")
	if err != nil {
		t.Fatalf("valid rw auth failed: %v", err)
	}
	clientRW.Close()

	// 2. Valid ro auth succeeds
	clientRO, err := dialTestSFTP(t, addr, "bob", "secret-ro-pass12")
	if err != nil {
		t.Fatalf("valid ro auth failed: %v", err)
	}
	clientRO.Close()

	// 3. Wrong password fails
	_, err = dialTestSFTP(t, addr, "alice", "wrong-pass")
	if err == nil {
		t.Fatalf("expected auth failure for wrong password")
	}

	// 4. Disabled user fails
	_, err = dialTestSFTP(t, addr, "disabled", "secret-dis-pass12")
	if err == nil {
		t.Fatalf("expected auth failure for disabled user")
	}

	// 5. User with mode "none" fails
	_, err = dialTestSFTP(t, addr, "none", "secret-none-pass12")
	if err == nil {
		t.Fatalf("expected auth failure for mode none")
	}

	// 6. Shell, exec, pty rejection
	sshCfg := &ssh.ClientConfig{
		User:            "alice",
		Auth:            []ssh.AuthMethod{ssh.Password("secret-rw-pass12")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	conn, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		t.Fatalf("ssh.Dial failed: %v", err)
	}
	defer conn.Close()

	// Session shell rejected
	sess, err := conn.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if err := sess.Shell(); err == nil {
		t.Fatalf("expected Shell() to be rejected")
	}
	sess.Close()

	// Session exec rejected
	sess, err = conn.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if err := sess.Run("id"); err == nil {
		t.Fatalf("expected Run('id') to be rejected")
	}
	sess.Close()

	// Session pty rejected
	sess, err = conn.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	if err := sess.RequestPty("vt100", 80, 24, nil); err == nil {
		t.Fatalf("expected RequestPty to be rejected")
	}
	sess.Close()
}

func TestLocalSFTPRWUploadTriggersHookAndSetsMode0644(t *testing.T) {
	rootDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "host_ed25519")

	var (
		uploadedMu   sync.Mutex
		uploadedPath string
		uploadedUser User
	)

	hooks := SFTPHooks{
		Authenticate: func(ctx context.Context, username, password string) (User, error) {
			if username == "alice" && password == "pwd123456789" {
				return User{ID: 42, Username: "alice", Enabled: true, FSAccessMode: "rw"}, nil
			}
			return User{}, errors.New("denied")
		},
		OnISOUploaded: func(ctx context.Context, user User, relPath string) {
			uploadedMu.Lock()
			defer uploadedMu.Unlock()
			uploadedPath = relPath
			uploadedUser = user
		},
	}

	server := NewLocalSFTPServer(rootDir, keyPath, hooks)
	defer server.Close()

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("server.Apply failed: %v", err)
	}
	_, addr := server.Running()

	client, err := dialTestSFTP(t, addr, "alice", "pwd123456789")
	if err != nil {
		t.Fatalf("dialTestSFTP failed: %v", err)
	}
	defer client.Close()

	content := []byte("BOOTABLE_ISO_TEST_IMAGE_DATA_123456789")
	handle, err := client.Open("test.iso", testFXFWrite|testFXFCreat|testFXFTrunc)
	if err != nil {
		t.Fatalf("Open test.iso failed: %v", err)
	}

	if err := client.Write(handle, 0, content); err != nil {
		t.Fatalf("Write test.iso failed: %v", err)
	}

	if err := client.CloseHandle(handle); err != nil {
		t.Fatalf("CloseHandle test.iso failed: %v", err)
	}

	// Verify file on disk
	diskPath := filepath.Join(rootDir, "test.iso")
	data, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("os.ReadFile failed: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("expected content %s, got %s", content, data)
	}

	info, err := os.Stat(diskPath)
	if err != nil {
		t.Fatalf("os.Stat failed: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected mode 0644, got %#o", info.Mode().Perm())
	}

	uploadedMu.Lock()
	gotPath := uploadedPath
	gotUser := uploadedUser
	uploadedMu.Unlock()

	if gotPath != "test.iso" {
		t.Fatalf("expected OnISOUploaded relPath='test.iso', got '%s'", gotPath)
	}
	if gotUser.ID != 42 || gotUser.Username != "alice" {
		t.Fatalf("expected user alice (42), got %+v", gotUser)
	}

	// Upload non-ISO file should NOT trigger OnISOUploaded
	uploadedMu.Lock()
	uploadedPath = ""
	uploadedMu.Unlock()

	hTxt, err := client.Open("readme.txt", testFXFWrite|testFXFCreat|testFXFTrunc)
	if err != nil {
		t.Fatalf("Open readme.txt failed: %v", err)
	}
	if err := client.Write(hTxt, 0, []byte("some text")); err != nil {
		t.Fatalf("Write readme.txt failed: %v", err)
	}
	if err := client.CloseHandle(hTxt); err != nil {
		t.Fatalf("CloseHandle readme.txt failed: %v", err)
	}

	uploadedMu.Lock()
	txtPath := uploadedPath
	uploadedMu.Unlock()
	if txtPath != "" {
		t.Fatalf("expected OnISOUploaded not called for .txt, but got '%s'", txtPath)
	}
}

func TestLocalSFTPROCannotWriteOrDelete(t *testing.T) {
	rootDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "host_ed25519")

	// Pre-create existing files and directories
	if err := os.WriteFile(filepath.Join(rootDir, "existing.iso"), []byte("EXISTING_DATA"), 0644); err != nil {
		t.Fatalf("setup existing.iso failed: %v", err)
	}
	if err := os.Mkdir(filepath.Join(rootDir, "subdir"), 0755); err != nil {
		t.Fatalf("setup subdir failed: %v", err)
	}

	hooks := SFTPHooks{
		Authenticate: func(ctx context.Context, username, password string) (User, error) {
			if username == "bob" && password == "secret-ro123" {
				return User{ID: 2, Username: "bob", Enabled: true, FSAccessMode: "ro"}, nil
			}
			return User{}, errors.New("denied")
		},
	}

	server := NewLocalSFTPServer(rootDir, keyPath, hooks)
	defer server.Close()

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("server.Apply failed: %v", err)
	}
	_, addr := server.Running()

	client, err := dialTestSFTP(t, addr, "bob", "secret-ro123")
	if err != nil {
		t.Fatalf("dialTestSFTP failed: %v", err)
	}
	defer client.Close()

	// 1. Reading directory works
	dh, err := client.Opendir("/")
	if err != nil {
		t.Fatalf("Opendir failed: %v", err)
	}
	names, err := client.Readdir(dh)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}
	_ = client.CloseHandle(dh)
	if len(names) == 0 {
		t.Fatalf("expected non-empty directory listing")
	}

	// 2. Reading file works
	fh, err := client.Open("existing.iso", testFXFRead)
	if err != nil {
		t.Fatalf("Open read existing.iso failed: %v", err)
	}
	data, err := client.Read(fh, 0, 100)
	if err != nil {
		t.Fatalf("Read existing.iso failed: %v", err)
	}
	if string(data) != "EXISTING_DATA" {
		t.Fatalf("expected EXISTING_DATA, got %s", data)
	}
	_ = client.CloseHandle(fh)

	// 3. Stat works
	if err := client.Stat("existing.iso"); err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	// 4. OPEN with write flag is denied
	_, err = client.Open("new.iso", testFXFWrite|testFXFCreat)
	assertStatusError(t, err, testFXPermissionDenied, "OPEN(WRITE) for ro user")

	_, err = client.Open("existing.iso", testFXFWrite)
	assertStatusError(t, err, testFXPermissionDenied, "OPEN(WRITE) existing for ro user")

	// 5. MKDIR is denied
	err = client.Mkdir("newdir")
	assertStatusError(t, err, testFXPermissionDenied, "MKDIR for ro user")

	// 6. REMOVE is denied
	err = client.Remove("existing.iso")
	assertStatusError(t, err, testFXPermissionDenied, "REMOVE for ro user")

	// 7. RENAME is denied
	err = client.Rename("existing.iso", "renamed.iso")
	assertStatusError(t, err, testFXPermissionDenied, "RENAME for ro user")

	// 8. RMDIR is denied
	err = client.Rmdir("subdir")
	assertStatusError(t, err, testFXPermissionDenied, "RMDIR for ro user")
}

func TestLocalSFTPMaintenanceBlocksWrites(t *testing.T) {
	rootDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "host_ed25519")

	if err := os.WriteFile(filepath.Join(rootDir, "existing.iso"), []byte("DATA"), 0644); err != nil {
		t.Fatalf("setup existing.iso failed: %v", err)
	}

	maintenanceActive := true

	hooks := SFTPHooks{
		Authenticate: func(ctx context.Context, username, password string) (User, error) {
			return User{ID: 1, Username: "alice", Enabled: true, FSAccessMode: "rw"}, nil
		},
		IsLocalMaintenance: func(ctx context.Context) bool {
			return maintenanceActive
		},
	}

	server := NewLocalSFTPServer(rootDir, keyPath, hooks)
	defer server.Close()

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("server.Apply failed: %v", err)
	}
	_, addr := server.Running()

	client, err := dialTestSFTP(t, addr, "alice", "any")
	if err != nil {
		t.Fatalf("dialTestSFTP failed: %v", err)
	}
	defer client.Close()

	// Reads still succeed during maintenance
	fh, err := client.Open("existing.iso", testFXFRead)
	if err != nil {
		t.Fatalf("expected Open(READ) to succeed during maintenance, got %v", err)
	}
	_ = client.CloseHandle(fh)

	// Writes fail during maintenance
	_, err = client.Open("existing.iso", testFXFWrite)
	assertStatusError(t, err, testFXPermissionDenied, "Open(WRITE) during maintenance")

	_, err = client.Open("new.iso", testFXFWrite|testFXFCreat)
	assertStatusError(t, err, testFXPermissionDenied, "Open(CREAT) during maintenance")

	err = client.Mkdir("maint_dir")
	assertStatusError(t, err, testFXPermissionDenied, "Mkdir during maintenance")

	err = client.Remove("existing.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Remove during maintenance")

	err = client.Rename("existing.iso", "renamed.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Rename during maintenance")
}

func TestLocalSFTPProtectedISOCannotBeRemovedOrOverwritten(t *testing.T) {
	rootDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "host_ed25519")

	if err := os.WriteFile(filepath.Join(rootDir, "used.iso"), []byte("USED_ISO_DATA"), 0644); err != nil {
		t.Fatalf("setup used.iso failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "free.iso"), []byte("FREE_ISO_DATA"), 0644); err != nil {
		t.Fatalf("setup free.iso failed: %v", err)
	}

	hooks := SFTPHooks{
		Authenticate: func(ctx context.Context, username, password string) (User, error) {
			return User{ID: 1, Username: "alice", Enabled: true, FSAccessMode: "rw"}, nil
		},
		IsISOProtected: func(ctx context.Context, relPath string) bool {
			return relPath == "used.iso"
		},
	}

	server := NewLocalSFTPServer(rootDir, keyPath, hooks)
	defer server.Close()

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("server.Apply failed: %v", err)
	}
	_, addr := server.Running()

	client, err := dialTestSFTP(t, addr, "alice", "any")
	if err != nil {
		t.Fatalf("dialTestSFTP failed: %v", err)
	}
	defer client.Close()

	// 1. REMOVE on protected ISO is denied
	err = client.Remove("used.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Remove(used.iso)")

	// 2. RENAME source protected ISO is denied
	err = client.Rename("used.iso", "target.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Rename(used.iso -> target.iso)")

	// 3. RENAME overwriting protected target ISO is denied
	err = client.Rename("free.iso", "used.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Rename(free.iso -> used.iso)")

	// 4. OPEN with WRITE or TRUNC on protected ISO is denied
	_, err = client.Open("used.iso", testFXFWrite)
	assertStatusError(t, err, testFXPermissionDenied, "Open(used.iso, WRITE)")

	_, err = client.Open("used.iso", testFXFWrite|testFXFTrunc)
	assertStatusError(t, err, testFXPermissionDenied, "Open(used.iso, WRITE|TRUNC)")

	// 5. Operations on free.iso succeed
	err = client.Remove("free.iso")
	if err != nil {
		t.Fatalf("expected Remove(free.iso) to succeed, got %v", err)
	}
}

func TestLocalSFTPSandboxBlocksTraversalSymlinksAndVetoFiles(t *testing.T) {
	rootDir := t.TempDir()
	outsideDir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "host_ed25519")

	secretOutside := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretOutside, []byte("SECRET"), 0644); err != nil {
		t.Fatalf("setup secret failed: %v", err)
	}

	// Symlink pointing outside root
	if err := os.Symlink(secretOutside, filepath.Join(rootDir, "escape_link")); err != nil {
		t.Fatalf("setup symlink failed: %v", err)
	}

	// Pre-create veto files on disk
	if err := os.WriteFile(filepath.Join(rootDir, ".DS_Store"), []byte("junk"), 0644); err != nil {
		t.Fatalf("setup .DS_Store failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "._foo.iso"), []byte("junk"), 0644); err != nil {
		t.Fatalf("setup ._foo.iso failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "valid.iso"), []byte("VALID"), 0644); err != nil {
		t.Fatalf("setup valid.iso failed: %v", err)
	}

	hooks := SFTPHooks{
		Authenticate: func(ctx context.Context, username, password string) (User, error) {
			return User{ID: 1, Username: "alice", Enabled: true, FSAccessMode: "rw"}, nil
		},
	}

	server := NewLocalSFTPServer(rootDir, keyPath, hooks)
	defer server.Close()

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("server.Apply failed: %v", err)
	}
	_, addr := server.Running()

	client, err := dialTestSFTP(t, addr, "alice", "any")
	if err != nil {
		t.Fatalf("dialTestSFTP failed: %v", err)
	}
	defer client.Close()

	// 1. Path traversal attempts
	_, err = client.Open("../escape.iso", testFXFRead)
	assertStatusError(t, err, testFXPermissionDenied, "Open(../escape.iso)")

	_, err = client.Open("/../escape.iso", testFXFRead)
	assertStatusError(t, err, testFXPermissionDenied, "Open(/../escape.iso)")

	err = client.Stat("../escape.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Stat(../escape.iso)")

	err = client.Remove("../escape.iso")
	assertStatusError(t, err, testFXPermissionDenied, "Remove(../escape.iso)")

	// 2. Symlink to outside root is blocked
	_, err = client.Open("escape_link", testFXFRead)
	assertStatusError(t, err, testFXPermissionDenied, "Open(escape_link)")

	err = client.Stat("escape_link")
	assertStatusError(t, err, testFXPermissionDenied, "Stat(escape_link)")

	// 3. Veto file creation/modification is blocked
	_, err = client.Open(".DS_Store", testFXFWrite|testFXFCreat)
	assertStatusError(t, err, testFXPermissionDenied, "Open(.DS_Store)")

	_, err = client.Open("._foo.iso", testFXFWrite|testFXFCreat)
	assertStatusError(t, err, testFXPermissionDenied, "Open(._foo.iso)")

	_, err = client.Open("Thumbs.db", testFXFWrite|testFXFCreat)
	assertStatusError(t, err, testFXPermissionDenied, "Open(Thumbs.db)")

	err = client.Mkdir(".DS_Store")
	assertStatusError(t, err, testFXPermissionDenied, "Mkdir(.DS_Store)")

	// 4. READDIR skips veto files (.DS_Store, ._foo.iso)
	dh, err := client.Opendir("/")
	if err != nil {
		t.Fatalf("Opendir failed: %v", err)
	}
	names, err := client.Readdir(dh)
	if err != nil {
		t.Fatalf("Readdir failed: %v", err)
	}
	_ = client.CloseHandle(dh)

	for _, name := range names {
		if name == ".DS_Store" || strings.HasPrefix(name, "._") || name == "Thumbs.db" {
			t.Fatalf("veto file %s was returned in Readdir listing: %v", name, names)
		}
	}
}

func TestLocalSFTPServerLifecycle(t *testing.T) {
	rootDir := t.TempDir()
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "host_ed25519")

	server := NewLocalSFTPServer(rootDir, keyPath, SFTPHooks{})
	defer server.Close()

	// Verify key was auto-generated with 0600 permissions
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("host key not generated: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected host key mode 0600, got %#o", info.Mode().Perm())
	}

	fp := server.HostKeyFingerprint()
	if !strings.HasPrefix(fp, "SHA256:") {
		t.Fatalf("expected fingerprint starting with SHA256:, got %s", fp)
	}

	running, addr := server.Running()
	if running || addr != "" {
		t.Fatalf("expected not running initially, got %v, %s", running, addr)
	}

	if err := server.Apply(true, "127.0.0.1", 0); err != nil {
		t.Fatalf("Apply(true) failed: %v", err)
	}
	running, addr = server.Running()
	if !running || addr == "" {
		t.Fatalf("expected running after Apply(true), got %v, %s", running, addr)
	}

	if err := server.Apply(false, "127.0.0.1", 0); err != nil {
		t.Fatalf("Apply(false) failed: %v", err)
	}
	running, addr = server.Running()
	if running || addr != "" {
		t.Fatalf("expected stopped after Apply(false), got %v, %s", running, addr)
	}
}

func assertStatusError(t *testing.T, err error, expectedCode uint32, op string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error for %s, got nil", op)
	}
	var se *sftpStatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected sftpStatusError for %s, got %T: %v", op, err, err)
	}
	if se.Code != expectedCode {
		t.Fatalf("expected status code %d for %s, got code=%d (msg=%s)", expectedCode, op, se.Code, se.Msg)
	}
}
