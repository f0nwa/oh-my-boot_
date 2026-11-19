package control

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

type SFTPHooks struct {
	Authenticate       func(ctx context.Context, username, password string) (User, error)
	IsLocalMaintenance func(ctx context.Context) bool
	IsISOProtected     func(ctx context.Context, relPath string) bool
	OnISOUploaded      func(ctx context.Context, user User, relPath string)
}

type LocalSFTPServer struct {
	rootDir             string
	hostKeyPath         string
	hooks               SFTPHooks
	sshConfig           *ssh.ServerConfig
	hostKeyFingerprint  string

	mu         sync.Mutex
	listener   net.Listener
	running    bool
	listenAddr string
	conns      map[net.Conn]struct{}
	cancel     context.CancelFunc
}

func NewLocalSFTPServer(rootDir, hostKeyPath string, hooks SFTPHooks) *LocalSFTPServer {
	cleanRoot := filepath.Clean(rootDir)
	if realRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = realRoot
	}
	s := &LocalSFTPServer{
		rootDir:     cleanRoot,
		hostKeyPath: filepath.Clean(hostKeyPath),
		hooks:       hooks,
		conns:       make(map[net.Conn]struct{}),
	}
	s.initHostKeyAndConfig()
	return s
}

func (s *LocalSFTPServer) initHostKeyAndConfig() {
	if err := os.MkdirAll(filepath.Dir(s.hostKeyPath), 0755); err != nil {
		// Ignore error if directory already exists
	}

	keyData, err := os.ReadFile(s.hostKeyPath)
	if err != nil {
		if os.IsNotExist(err) {
			_, priv, genErr := ed25519.GenerateKey(rand.Reader)
			if genErr == nil {
				block, mErr := ssh.MarshalPrivateKey(priv, "")
				if mErr != nil {
					der, _ := x509.MarshalPKCS8PrivateKey(priv)
					block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
				}
				keyData = pem.EncodeToMemory(block)
				_ = os.WriteFile(s.hostKeyPath, keyData, 0600)
			}
		}
	}

	authPassword := func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if s.hooks.Authenticate == nil {
			log.Printf("sftp: auth failed: hooks not configured")
			return nil, errors.New("auth not configured")
		}
		user, err := s.hooks.Authenticate(context.Background(), conn.User(), string(password))
		if err != nil {
			log.Printf("sftp: auth failed for user %q from %s: %v", conn.User(), conn.RemoteAddr(), err)
			return nil, err
		}
		if !user.Enabled || (user.FSAccessMode != "ro" && user.FSAccessMode != "rw") {
			log.Printf("sftp: access denied for user %q (enabled=%v, mode=%s) from %s", user.Username, user.Enabled, user.FSAccessMode, conn.RemoteAddr())
			return nil, errors.New("access denied")
		}
		log.Printf("sftp: user %q (id=%d, mode=%s) authenticated from %s", user.Username, user.ID, user.FSAccessMode, conn.RemoteAddr())
		return &ssh.Permissions{
			Extensions: map[string]string{
				"username": user.Username,
				"user_id":  fmt.Sprint(user.ID),
				"mode":     user.FSAccessMode,
			},
		}, nil
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: authPassword,
		KeyboardInteractiveCallback: func(conn ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			answers, err := client(conn.User(), "", []string{"Password: "}, []bool{false})
			if err != nil || len(answers) == 0 {
				return nil, errors.New("keyboard-interactive auth failed")
			}
			return authPassword(conn, []byte(answers[0]))
		},
	}

	if len(keyData) > 0 {
		signer, err := ssh.ParsePrivateKey(keyData)
		if err == nil {
			cfg.AddHostKey(signer)
			s.hostKeyFingerprint = ssh.FingerprintSHA256(signer.PublicKey())
		}
	}

	s.sshConfig = cfg
}

func (s *LocalSFTPServer) HostKeyFingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostKeyFingerprint
}

func (s *LocalSFTPServer) Running() (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running, s.listenAddr
}

func (s *LocalSFTPServer) Apply(enabled bool, host string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !enabled {
		if s.running {
			s.running = false
			s.listenAddr = ""
			if s.listener != nil {
				_ = s.listener.Close()
				s.listener = nil
			}
			if s.cancel != nil {
				s.cancel()
				s.cancel = nil
			}
			for c := range s.conns {
				_ = c.Close()
			}
			s.conns = make(map[net.Conn]struct{})
		}
		return nil
	}

	target := net.JoinHostPort(host, strconv.Itoa(port))
	if s.running && s.listener != nil && port != 0 && s.listenAddr == target {
		return nil
	}

	// Close existing listener before starting new one
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	for c := range s.conns {
		_ = c.Close()
	}
	s.conns = make(map[net.Conn]struct{})

	l, err := net.Listen("tcp", target)
	if err != nil {
		s.running = false
		s.listenAddr = ""
		return err
	}

	s.listener = l
	s.listenAddr = l.Addr().String()
	s.running = true

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	go s.acceptLoop(ctx, l)
	return nil
}

func (s *LocalSFTPServer) Close() error {
	return s.Apply(false, "", 0)
}

func (s *LocalSFTPServer) trackConn(c net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[c] = struct{}{}
	} else {
		delete(s.conns, c)
	}
}

func (s *LocalSFTPServer) acceptLoop(ctx context.Context, l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.mu.Lock()
				running := s.running
				s.mu.Unlock()
				if !running {
					return
				}
				continue
			}
		}

		s.trackConn(conn, true)
		go func(c net.Conn) {
			defer s.trackConn(c, false)
			defer c.Close()
			s.handleSSHConn(ctx, c)
		}(conn)
	}
}

func (s *LocalSFTPServer) handleSSHConn(ctx context.Context, conn net.Conn) {
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshConfig)
	if err != nil {
		log.Printf("sftp: handshake failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)

	user := User{
		Username:     sshConn.Permissions.Extensions["username"],
		FSAccessMode: sshConn.Permissions.Extensions["mode"],
		Enabled:      true,
	}
	if idStr := sshConn.Permissions.Extensions["user_id"]; idStr != "" {
		var id int64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err == nil {
			user.ID = id
		}
	}

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			log.Printf("sftp: rejected non-session channel %q from %s", newChannel.ChannelType(), conn.RemoteAddr())
			_ = newChannel.Reject(ssh.Prohibited, "only session channel is allowed")
			continue
		}

		channel, requests, err := newChannel.Accept()
		if err != nil {
			log.Printf("sftp: accept channel failed from %s: %v", conn.RemoteAddr(), err)
			continue
		}

		go s.handleSessionRequests(ctx, channel, requests, user)
	}
}

func (s *LocalSFTPServer) handleSessionRequests(ctx context.Context, channel ssh.Channel, requests <-chan *ssh.Request, user User) {
	for req := range requests {
		switch req.Type {
		case "subsystem":
			subsystem, _, _ := getSFTPString(req.Payload)
			if subsystem == "sftp" {
				_ = req.Reply(true, nil)
				log.Printf("sftp: session opened for %q", user.Username)
				s.handleSFTP(ctx, channel, user)
				return
			}
			log.Printf("sftp: rejected subsystem %q from %q", subsystem, user.Username)
			_ = req.Reply(false, nil)
		default:
			log.Printf("sftp: rejected request type %q from %q", req.Type, user.Username)
			_ = req.Reply(false, nil)
		}
	}
}

type sftpHandleEntry struct {
	id         string
	file       *os.File
	relPath    string
	absPath    string
	isDir      bool
	dirEntries []os.DirEntry
	dirOffset  int
	isWrite    bool
	user       User
}

func (s *LocalSFTPServer) sanitizePath(clientPath string) (string, string, uint32) {
	return s.sanitizePathInternal(clientPath, false)
}

func (s *LocalSFTPServer) sanitizePathAllowVeto(clientPath string) (string, string, uint32) {
	return s.sanitizePathInternal(clientPath, true)
}

func (s *LocalSFTPServer) sanitizePathInternal(clientPath string, allowVeto bool) (string, string, uint32) {
	toSlash := filepath.ToSlash(clientPath)

	for _, part := range strings.Split(toSlash, "/") {
		if part == ".." {
			return "", "", sftpStatusPermissionDenied
		}
		if !allowVeto && isVetoName(part) {
			return "", "", sftpStatusPermissionDenied
		}
	}
	if strings.HasPrefix(toSlash, "../") || toSlash == ".." {
		return "", "", sftpStatusPermissionDenied
	}

	cleanRel := filepath.Clean(strings.TrimPrefix(toSlash, "/"))
	if cleanRel == "." || cleanRel == "" {
		cleanRel = ""
	}

	absPath := filepath.Clean(filepath.Join(s.rootDir, cleanRel))

	rel, err := filepath.Rel(s.rootDir, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(rel, "../") {
		return "", "", sftpStatusPermissionDenied
	}

	// Verify symlinks do not escape rootDir
	realRoot := s.rootDir
	if r, err := filepath.EvalSymlinks(s.rootDir); err == nil {
		realRoot = r
	}

	if target, err := filepath.EvalSymlinks(absPath); err == nil {
		targetRel, err := filepath.Rel(realRoot, target)
		if err != nil || targetRel == ".." || strings.HasPrefix(targetRel, ".."+string(filepath.Separator)) || strings.HasPrefix(targetRel, "../") {
			return "", "", sftpStatusPermissionDenied
		}
	} else {
		// If target doesn't exist yet, check nearest existing ancestor dir
		curr := filepath.Dir(absPath)
		for {
			targetParent, err := filepath.EvalSymlinks(curr)
			if err == nil {
				parentRel, err := filepath.Rel(realRoot, targetParent)
				if err != nil || parentRel == ".." || strings.HasPrefix(parentRel, ".."+string(filepath.Separator)) || strings.HasPrefix(parentRel, "../") {
					return "", "", sftpStatusPermissionDenied
				}
				break
			}
			next := filepath.Dir(curr)
			if next == curr || len(curr) <= len(s.rootDir) {
				break
			}
			curr = next
		}
	}

	return cleanRel, absPath, sftpStatusOK
}

func isVetoName(name string) bool {
	base := filepath.Base(name)
	if base == ".DS_Store" || base == "Thumbs.db" || base == "desktop.ini" {
		return true
	}
	if strings.HasPrefix(base, "._") {
		return true
	}
	return false
}

func (s *LocalSFTPServer) canWrite(ctx context.Context, user User) bool {
	if user.FSAccessMode != "rw" {
		return false
	}
	if s.hooks.IsLocalMaintenance != nil && s.hooks.IsLocalMaintenance(ctx) {
		return false
	}
	return true
}

func (s *LocalSFTPServer) isProtected(ctx context.Context, relPath string) bool {
	if s.hooks.IsISOProtected == nil {
		return false
	}
	clean := filepath.Clean(strings.TrimPrefix(filepath.ToSlash(relPath), "/"))
	return s.hooks.IsISOProtected(ctx, clean)
}

func (s *LocalSFTPServer) handleSFTP(ctx context.Context, ch ssh.Channel, user User) {
	defer ch.Close()

	handles := make(map[string]*sftpHandleEntry)
	var handleSeq uint64

	defer func() {
		for _, h := range handles {
			if h.file != nil {
				_ = h.file.Close()
			}
		}
	}()

	for {
		typ, payload, err := readSFTPPacket(ch)
		if err != nil {
			log.Printf("sftp: session ended for %q: %v", user.Username, err)
			return
		}
		log.Printf("sftp: packet %d len %d from %q", typ, len(payload), user.Username)

		switch typ {
		case sftpFXPInit:
			_ = sendSFTPPacket(ch, sftpFXPVersion, []byte{0, 0, 0, 3})

		case sftpFXPRealpath:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			p, _, _ := getSFTPString(rest)
			if p == "" || p == "." || p == "/" {
				p = "/"
			} else {
				rel, _, status := s.sanitizePath(p)
				if status != sftpStatusOK {
					_ = sendSFTPStatus(ch, id, status, "path denied")
					continue
				}
				p = "/" + rel
			}
			_ = sendSFTPName(ch, id, []sftpNameEntry{{
				filename: p,
				longname: p,
				attrs:    sftpAttrs{flags: 0},
			}})

		case sftpFXPOpen:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			pathStr, rest, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad path")
				continue
			}
			flags, _, err := getUint32(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad flags")
				continue
			}

			rel, abs, status := s.sanitizePath(pathStr)
			if status != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, status, "permission denied")
				continue
			}

			isWriteReq := (flags & (sftpFXFWrite | sftpFXFCreat | sftpFXFTrunc | sftpFXFAppend)) != 0
			if isWriteReq {
				if !s.canWrite(ctx, user) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
					continue
				}
				if s.isProtected(ctx, rel) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "protected iso")
					continue
				}
			}

			osFlags := 0
			if (flags&sftpFXFRead != 0) && (flags&sftpFXFWrite != 0) {
				osFlags = os.O_RDWR
			} else if flags&sftpFXFWrite != 0 {
				osFlags = os.O_WRONLY
			} else {
				osFlags = os.O_RDONLY
			}
			if flags&sftpFXFAppend != 0 {
				osFlags |= os.O_APPEND
			}
			if flags&sftpFXFCreat != 0 {
				osFlags |= os.O_CREATE
			}
			if flags&sftpFXFTrunc != 0 {
				osFlags |= os.O_TRUNC
			}
			if flags&sftpFXFExcl != 0 {
				osFlags |= os.O_EXCL
			}

			if flags&sftpFXFCreat != 0 {
				parent := filepath.Dir(abs)
				_ = os.MkdirAll(parent, 0770)
				curr := parent
				for curr != s.rootDir && len(curr) > len(s.rootDir) {
					_ = os.Chmod(curr, 0770|os.ModeSetgid)
					curr = filepath.Dir(curr)
				}
			}

			file, err := os.OpenFile(abs, osFlags, 0664)
			if flags&sftpFXFCreat != 0 && err == nil {
				_ = os.Chmod(abs, 0664)
			}
			if err != nil {
				if os.IsNotExist(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusNoSuchFile, "no such file")
				} else if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}

			seq := atomic.AddUint64(&handleSeq, 1)
			hID := fmt.Sprintf("f%d", seq)
			handles[hID] = &sftpHandleEntry{
				id:      hID,
				file:    file,
				relPath: rel,
				absPath: abs,
				isWrite: isWriteReq,
				user:    user,
			}
			_ = sendSFTPHandle(ch, id, hID)

		case sftpFXPClose:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			hID, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad handle")
				continue
			}
			h, ok := handles[hID]
			if !ok {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, "invalid handle")
				continue
			}
			delete(handles, hID)
			if h.file != nil {
				_ = h.file.Close()
				if h.isWrite {
					_ = os.Chmod(h.absPath, 0664)
					if strings.EqualFold(filepath.Ext(h.relPath), ".iso") {
						if s.hooks.OnISOUploaded != nil {
							s.hooks.OnISOUploaded(ctx, h.user, h.relPath)
						}
					}
				}
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		case sftpFXPRead:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			hID, rest, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad handle")
				continue
			}
			offset, rest, err := getUint64(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad offset")
				continue
			}
			length, _, err := getUint32(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad length")
				continue
			}
			h, ok := handles[hID]
			if !ok || h.file == nil {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, "invalid handle")
				continue
			}
			if length > 256*1024 {
				length = 256 * 1024
			}
			buf := make([]byte, length)
			n, err := h.file.ReadAt(buf, int64(offset))
			if n > 0 {
				_ = sendSFTPData(ch, id, buf[:n])
			} else if err == io.EOF || n == 0 {
				_ = sendSFTPStatus(ch, id, sftpStatusEOF, "eof")
			} else {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
			}

		case sftpFXPWrite:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			hID, rest, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad handle")
				continue
			}
			offset, rest, err := getUint64(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad offset")
				continue
			}
			dataStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad data")
				continue
			}
			h, ok := handles[hID]
			if !ok || h.file == nil || !h.isWrite {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			if !s.canWrite(ctx, user) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			data := []byte(dataStr)
			_, err = h.file.WriteAt(data, int64(offset))
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				continue
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		case sftpFXPOpendir:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			pathStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad path")
				continue
			}
			rel, abs, status := s.sanitizePath(pathStr)
			if status != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, status, "permission denied")
				continue
			}
			entries, err := os.ReadDir(abs)
			if err != nil {
				if os.IsNotExist(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusNoSuchFile, "no such directory")
				} else if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}
			var filtered []os.DirEntry
			for _, e := range entries {
				if isVetoName(e.Name()) {
					continue
				}
				filtered = append(filtered, e)
			}
			seq := atomic.AddUint64(&handleSeq, 1)
			hID := fmt.Sprintf("d%d", seq)
			handles[hID] = &sftpHandleEntry{
				id:         hID,
				relPath:    rel,
				absPath:    abs,
				isDir:      true,
				dirEntries: filtered,
				dirOffset:  0,
				user:       user,
			}
			_ = sendSFTPHandle(ch, id, hID)

		case sftpFXPReaddir:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			hID, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad handle")
				continue
			}
			h, ok := handles[hID]
			if !ok || !h.isDir {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, "invalid handle")
				continue
			}
			if h.dirOffset >= len(h.dirEntries) {
				_ = sendSFTPStatus(ch, id, sftpStatusEOF, "eof")
				continue
			}
			batchSize := 64
			end := h.dirOffset + batchSize
			if end > len(h.dirEntries) {
				end = len(h.dirEntries)
			}
			batch := h.dirEntries[h.dirOffset:end]
			h.dirOffset = end
			var names []sftpNameEntry
			for _, e := range batch {
				fi, err := e.Info()
				if err != nil {
					continue
				}
				names = append(names, sftpNameEntry{
					filename: e.Name(),
					longname: formatLongname(e.Name(), fi),
					attrs:    attrsFromFileInfo(fi),
				})
			}
			_ = sendSFTPName(ch, id, names)

		case sftpFXPStat, sftpFXPLstat:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			pathStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad path")
				continue
			}
			_, abs, status := s.sanitizePath(pathStr)
			if status != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, status, "permission denied")
				continue
			}
			var fi os.FileInfo
			if typ == sftpFXPStat {
				fi, err = os.Stat(abs)
			} else {
				fi, err = os.Lstat(abs)
			}
			if err != nil {
				if os.IsNotExist(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusNoSuchFile, "no such file")
				} else if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}
			_ = sendSFTPAttrs(ch, id, attrsFromFileInfo(fi))

		case sftpFXPFstat:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			hID, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad handle")
				continue
			}
			h, ok := handles[hID]
			if !ok || h.file == nil {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, "invalid handle")
				continue
			}
			fi, err := h.file.Stat()
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				continue
			}
			_ = sendSFTPAttrs(ch, id, attrsFromFileInfo(fi))

		case sftpFXPRemove:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			pathStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad path")
				continue
			}
			rel, abs, status := s.sanitizePath(pathStr)
			if status != sftpStatusOK && isVetoName(filepath.Base(pathStr)) {
				rel, abs, status = s.sanitizePathAllowVeto(pathStr)
			}
			if status != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, status, "permission denied")
				continue
			}
			if !s.canWrite(ctx, user) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			if s.isProtected(ctx, rel) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "protected iso")
				continue
			}
			err = os.Remove(abs)
			if err != nil {
				if os.IsNotExist(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusNoSuchFile, "no such file")
				} else if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		case sftpFXPRename:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			oldPathStr, rest, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad old path")
				continue
			}
			newPathStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad new path")
				continue
			}
			oldRel, oldAbs, statusOld := s.sanitizePath(oldPathStr)
			if statusOld != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, statusOld, "permission denied")
				continue
			}
			newRel, newAbs, statusNew := s.sanitizePath(newPathStr)
			if statusNew != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, statusNew, "permission denied")
				continue
			}
			if !s.canWrite(ctx, user) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			if s.isProtected(ctx, oldRel) || s.isProtected(ctx, newRel) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "protected iso")
				continue
			}
			err = os.Rename(oldAbs, newAbs)
			if err != nil {
				if os.IsNotExist(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusNoSuchFile, "no such file")
				} else if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		case sftpFXPMkdir:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			pathStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad path")
				continue
			}
			_, abs, status := s.sanitizePath(pathStr)
			if status != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, status, "permission denied")
				continue
			}
			if !s.canWrite(ctx, user) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			err = os.Mkdir(abs, 0770)
			if err == nil {
				_ = os.Chmod(abs, 0770|os.ModeSetgid)
			}
			if err != nil {
				if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		case sftpFXPRmdir:
			id, rest, err := getUint32(payload)
			if err != nil {
				return
			}
			pathStr, _, err := getSFTPString(rest)
			if err != nil {
				_ = sendSFTPStatus(ch, id, sftpStatusBadMessage, "bad path")
				continue
			}
			_, abs, status := s.sanitizePath(pathStr)
			if status != sftpStatusOK {
				_ = sendSFTPStatus(ch, id, status, "permission denied")
				continue
			}
			if !s.canWrite(ctx, user) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			err = os.Remove(abs)
			if err != nil {
				if entries, readErr := os.ReadDir(abs); readErr == nil && len(entries) > 0 {
					allVeto := true
					for _, e := range entries {
						if e.IsDir() || !isVetoName(e.Name()) {
							allVeto = false
							break
						}
					}
					if allVeto {
						for _, e := range entries {
							_ = os.Remove(filepath.Join(abs, e.Name()))
						}
						err = os.Remove(abs)
					}
				}
			}
			if err != nil {
				if os.IsNotExist(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusNoSuchFile, "no such file")
				} else if os.IsPermission(err) {
					_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				} else {
					_ = sendSFTPStatus(ch, id, sftpStatusFailure, err.Error())
				}
				continue
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		case sftpFXPSetstat, sftpFXPFsetstat:
			id, _, err := getUint32(payload)
			if err != nil {
				return
			}
			if !s.canWrite(ctx, user) {
				_ = sendSFTPStatus(ch, id, sftpStatusPermissionDenied, "permission denied")
				continue
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOK, "ok")

		default:
			id, _, err := getUint32(payload)
			if err != nil {
				return
			}
			_ = sendSFTPStatus(ch, id, sftpStatusOpUnsupported, "unsupported operation")
		}
	}
}
