package control

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"omb/internal/agent"
)

type fileServerView struct {
	SMBEnabled         bool
	SFTPEnabled        bool
	SFTPPort           int
	SMBAddress         string
	SFTPAddress        string
	HostKeyFingerprint string
	ActiveUsersCount   int
	ROUsersCount       int
	RWUsersCount       int
	IsLocalMaintenance bool
}

func (s *Server) localFileServerState(ctx context.Context, hostHeader string) fileServerView {
	var view fileServerView
	view.SFTPPort = DefaultSFTPPort
	if s.SFTPServer != nil {
		view.HostKeyFingerprint = s.SFTPServer.HostKeyFingerprint()
	}
	view.IsLocalMaintenance = s.IsLocalMaintenance(ctx)

	if s.Store != nil {
		if settings, err := s.Store.FileServerSettings(ctx); err == nil {
			view.SMBEnabled = settings.SMBEnabled
			view.SFTPEnabled = settings.SFTPEnabled
			if settings.SFTPPort > 0 {
				view.SFTPPort = settings.SFTPPort
			}
		}
		if users, err := s.Store.FileServerUsers(ctx); err == nil {
			for _, u := range users {
				if u.Enabled && u.HasPassword {
					if u.Mode == FSAccessRO {
						view.ROUsersCount++
						view.ActiveUsersCount++
					} else if u.Mode == FSAccessRW {
						view.RWUsersCount++
						view.ActiveUsersCount++
					}
				}
			}
		}
	}

	host := hostHeader
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || host == "0.0.0.0" {
		if s.ListenAddr != "" {
			if lh, _, err := net.SplitHostPort(s.ListenAddr); err == nil && lh != "" && lh != "0.0.0.0" {
				host = lh
			}
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}

	view.SMBAddress = fmt.Sprintf(`\\%s\iso`, host)
	view.SFTPAddress = fmt.Sprintf("sftp://%s:%d/", host, view.SFTPPort)

	return view
}

func (v fileServerView) UserSFTPAddress(username string) string {
	if v.SFTPAddress == "" {
		return ""
	}
	if username == "" {
		return v.SFTPAddress
	}
	return strings.Replace(v.SFTPAddress, "sftp://", "sftp://"+username+"@", 1)
}

func (s *Server) localStorageID(ctx context.Context) int64 {
	if s == nil {
		return 1
	}
	if id := s.localID.Load(); id > 0 {
		return id
	}
	if s.Images != nil {
		if catalog, err := s.Images.Catalog(ctx); err == nil {
			for _, st := range catalog.Storages {
				if st.Kind == "local" {
					s.localID.Store(st.ID)
					return st.ID
				}
			}
		}
	}
	return 1
}

func (s *Server) IsLocalMaintenance(ctx context.Context) bool {
	lookup := s.loadMaintenanceLookup(ctx)
	if len(lookup.locks) == 0 {
		return false
	}
	localID := s.localStorageID(ctx)
	if localID > 0 {
		if _, ok := lookup.StorageLock(localID); ok {
			return true
		}
	}
	return false
}

func (s *Server) IsLocalISOProtected(ctx context.Context, relPath string) bool {
	localID := s.localStorageID(ctx)
	relPath = strings.TrimPrefix(filepath.Clean(relPath), "/")
	if _, _, locked := s.loadMaintenanceLookup(ctx).ImageLock(localID, relPath); locked {
		return true
	}
	if s.Store == nil {
		return false
	}
	profiles, err := s.Store.WindowsProfiles(ctx)
	if err != nil {
		return false
	}
	sources, err := s.libraryMenuReferences(ctx)
	if err != nil {
		return false
	}
	uses := imageDownloadsBySource(profiles, sources)
	return len(uses[imageAuthorKey(localID, relPath)]) > 0
}

func (s *Server) OnISOUploaded(ctx context.Context, user User, relPath string) {
	localID := s.localStorageID(ctx)
	relPath = strings.TrimPrefix(filepath.Clean(relPath), "/")
	if s.Store != nil {
		_ = s.Store.Audit(ctx, user.ID, "iso.upload", "ok", fmt.Sprintf("%d %s sftp", localID, relPath))
	}
	s.InvalidateResourceAuthorsCache()
	if s.Images != nil {
		_ = s.Images.Scan(ctx, localID)
	}
}

func (s *Server) SyncLocalFileServer(ctx context.Context) error {
	var settings FileServerSettings
	if s.Store != nil {
		var err error
		settings, err = s.Store.FileServerSettings(ctx)
		if err != nil {
			return fmt.Errorf("read file server settings: %w", err)
		}
	}

	var users []FileServerUser
	if s.Store != nil {
		var err error
		users, err = s.Store.FileServerUsers(ctx)
		if err != nil {
			return fmt.Errorf("read file server users: %w", err)
		}
	}

	isLocalMaint := s.IsLocalMaintenance(ctx)

	var smbUsers []agent.LocalSMBUser
	for _, u := range users {
		if u.Enabled && u.HasPassword && (u.Mode == FSAccessRO || u.Mode == FSAccessRW) {
			smbUsers = append(smbUsers, agent.LocalSMBUser{
				ID:       u.ID,
				Username: u.Username,
				Mode:     u.Mode,
			})
		}
	}

	listenHost := ""
	if s.ListenAddr != "" {
		if host, _, err := net.SplitHostPort(s.ListenAddr); err == nil {
			listenHost = host
		} else {
			listenHost = s.ListenAddr
		}
		if listenHost == "0.0.0.0" {
			listenHost = ""
		}
	}

	var smbErr, sftpErr error
	if s.LocalSMB != nil {
		cfg := agent.LocalSMBConfig{
			Enabled:     settings.SMBEnabled,
			ListenIP:    listenHost,
			ReadOnlyAll: isLocalMaint,
			Users:       smbUsers,
		}
		if err := s.LocalSMB.ApplyLocalSMB(ctx, cfg); err != nil {
			smbErr = fmt.Errorf("apply local smb: %w", err)
		}
	}

	if s.SFTPServer != nil {
		port := settings.SFTPPort
		if port == 0 {
			port = DefaultSFTPPort
		}
		if err := s.SFTPServer.Apply(settings.SFTPEnabled, listenHost, port); err != nil {
			sftpErr = fmt.Errorf("apply local sftp: %w", err)
		}
	}

	return errors.Join(smbErr, sftpErr)
}

func (s *Server) fileServerSettingsSave(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	smbEnabled := r.PostFormValue("smb_enabled") == "on" || r.PostFormValue("smb_enabled") == "true" || r.PostFormValue("smb_enabled") == "1"
	sftpEnabled := r.PostFormValue("sftp_enabled") == "on" || r.PostFormValue("sftp_enabled") == "true" || r.PostFormValue("sftp_enabled") == "1"

	portStr := strings.TrimSpace(r.PostFormValue("sftp_port"))
	port := DefaultSFTPPort
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 1024 || p > 65535 {
			http.Error(w, "Неверный порт SFTP: порт должен быть в диапазоне от 1024 до 65535", http.StatusBadRequest)
			return
		}
		port = p
	}

	settings := FileServerSettings{
		SMBEnabled:  smbEnabled,
		SFTPEnabled: sftpEnabled,
		SFTPPort:    port,
	}

	if err := ValidateFileServerSettings(settings); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if s.Store != nil {
		if err := s.Store.SetFileServerSettings(r.Context(), settings); err != nil {
			_ = s.Store.Audit(r.Context(), info.User.ID, "storage.file_server", "error", fmt.Sprintf("smb=%t sftp=%t port=%d: %v", smbEnabled, sftpEnabled, port, err))
			http.Error(w, "Не удалось сохранить настройки файлового сервера: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	syncErr := s.SyncLocalFileServer(r.Context())
	detail := fmt.Sprintf("smb=%t sftp=%t port=%d", smbEnabled, sftpEnabled, port)
	if syncErr != nil {
		if s.Store != nil {
			_ = s.Store.Audit(r.Context(), info.User.ID, "storage.file_server", "sync_error", detail+": "+syncErr.Error())
		}
		s.recordSystemEvent(r.Context(), SystemEvent{
			Severity:  "error",
			Category:  "task",
			SourceKey: "storage:file_server",
			Title:     "Не удалось синхронизировать файловый сервер",
			Detail:    syncErr.Error(),
			Link:      "/storages",
		})
	} else {
		if s.Store != nil {
			_ = s.Store.Audit(r.Context(), info.User.ID, "storage.file_server", "ok", detail)
		}
		s.recordSystemEvent(r.Context(), SystemEvent{
			Severity:  "info",
			Category:  "task",
			SourceKey: "storage:file_server",
			Title:     "Настройки файлового сервера обновлены",
			Detail:    fmt.Sprintf("smb=%t, sftp=%t, port=%d", smbEnabled, sftpEnabled, port),
			Link:      "/storages",
		})
	}

	msg := "Настройки файлового сервера сохранены"
	if syncErr != nil {
		msg = "Настройки файлового сервера сохранены, но синхронизация завершилась с ошибкой"
	}
	http.Redirect(w, r, "/storages?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) userFileAccessSave(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	idStr := strings.TrimSpace(r.PostFormValue("user_id"))
	if idStr == "" {
		idStr = strings.TrimSpace(r.PostFormValue("id"))
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный ID пользователя", http.StatusBadRequest)
		return
	}

	mode := strings.TrimSpace(r.PostFormValue("mode"))
	if mode == "" {
		mode = strings.TrimSpace(r.PostFormValue("fs_access_mode"))
	}
	if !ValidFSAccessMode(mode) {
		http.Error(w, "Неверный режим доступа к файловому серверу", http.StatusBadRequest)
		return
	}

	password := r.PostFormValue("password")
	if password == "" {
		password = r.PostFormValue("fs_password")
	}

	if password != "" && utf8.RuneCountInString(password) < 12 {
		http.Error(w, "Файловый пароль должен содержать не менее 12 символов", http.StatusBadRequest)
		return
	}

	var username string
	if s.Store != nil {
		u, err := s.Store.SetUserFileAccess(r.Context(), id, mode, password)
		if err != nil {
			if errors.Is(err, ErrFSPasswordRequired) {
				http.Error(w, "Для первого включения доступа требуется пароль от 12 символов", http.StatusBadRequest)
				return
			}
			if errors.Is(err, ErrInvalidFSAccessMode) {
				http.Error(w, "Неверный режим доступа", http.StatusBadRequest)
				return
			}
			_ = s.Store.Audit(r.Context(), info.User.ID, "user.file_access", "error", fmt.Sprintf("id=%d mode=%s: %v", id, mode, err))
			http.Error(w, "Не удалось обновить доступ к файловому серверу: "+err.Error(), http.StatusBadRequest)
			return
		}
		username = u
	}

	if password != "" && (mode == FSAccessRO || mode == FSAccessRW) && s.LocalSMB != nil {
		if err := s.LocalSMB.SetLocalSMBPassword(r.Context(), agent.LocalSMBPasswordRequest{
			ID:       id,
			Username: username,
			Password: password,
		}); err != nil {
			log.Printf("set local smb password for user %s: %v", username, err)
		}
	}

	_ = s.SyncLocalFileServer(r.Context())

	if s.Store != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "user.file_access", "ok", fmt.Sprintf("id=%d username=%s mode=%s", id, username, mode))
	}

	msg := fmt.Sprintf("Доступ к файловому серверу для «%s» обновлён", username)
	if mode == FSAccessNone {
		msg = fmt.Sprintf("Доступ к файловому серверу для «%s» отключён", username)
	}
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) accountFilePassword(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	if info.User.FSAccessMode != FSAccessRO && info.User.FSAccessMode != FSAccessRW {
		http.Error(w, "Доступ к файловому серверу не разрешён", http.StatusForbidden)
		return
	}

	password := r.PostFormValue("password")
	if password == "" {
		password = r.PostFormValue("new_password")
	}

	confirm := r.PostFormValue("confirm_password")
	if password != confirm {
		http.Error(w, "Пароли не совпадают", http.StatusBadRequest)
		return
	}

	if utf8.RuneCountInString(password) < 12 {
		http.Error(w, "Файловый пароль должен содержать не менее 12 символов", http.StatusBadRequest)
		return
	}

	var username string
	if s.Store != nil {
		u, err := s.Store.SetOwnFilePassword(r.Context(), info.User.ID, password)
		if err != nil {
			if errors.Is(err, ErrFSNotEnabled) {
				http.Error(w, "Доступ к файловому серверу не разрешён", http.StatusForbidden)
				return
			}
			_ = s.Store.Audit(r.Context(), info.User.ID, "account.file_password", "error", fmt.Sprintf("id=%d: %v", info.User.ID, err))
			http.Error(w, "Не удалось изменить файловый пароль: "+err.Error(), http.StatusBadRequest)
			return
		}
		username = u
	}

	if s.LocalSMB != nil {
		if err := s.LocalSMB.SetLocalSMBPassword(r.Context(), agent.LocalSMBPasswordRequest{
			ID:       info.User.ID,
			Username: username,
			Password: password,
		}); err != nil {
			log.Printf("set local smb password for user %s: %v", username, err)
		}
	}

	if s.Store != nil {
		_ = s.Store.Audit(r.Context(), info.User.ID, "account.file_password", "ok", fmt.Sprintf("id=%d", info.User.ID))
	}

	http.Redirect(w, r, "/account?message="+url.QueryEscape("Пароль SMB/SFTP изменён"), http.StatusSeeOther)
}
