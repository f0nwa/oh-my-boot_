package control

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/images"
)

type Storage interface {
	Login(context.Context, string, string) (User, string, error)
	Session(context.Context, string) (User, error)
	Logout(context.Context, string) error
	Audit(context.Context, int64, string, string, string) error
	AuditList(context.Context) ([]AuditEvent, error)
	Users(context.Context) ([]User, error)
	CreateUser(context.Context, string, string, string) error
	ChangeUser(context.Context, int64, int64, string, bool) error
	ResetPassword(context.Context, int64, string) error
	MenuVersions(context.Context) ([]MenuVersion, error)
	MenuByID(context.Context, int64) (MenuVersion, error)
	SaveActiveMenu(context.Context, int64, MenuVersion) error
	CreateWindowsProfile(context.Context, WindowsProfileSource) (WindowsProfile, error)
	WindowsProfile(context.Context, int64) (WindowsProfile, error)
	WindowsProfiles(context.Context) ([]WindowsProfile, error)
	SetWindowsProfileResult(context.Context, int64, string, string, string) error
}

type ImagesAPI interface {
	Catalog(context.Context) (images.Catalog, error)
	Scan(context.Context, int64) error
	CreateSMB(context.Context, images.ManagedStorageInput) (images.Storage, error)
	MarkSMBError(context.Context, int64) error
	MarkSMBScanError(context.Context, int64) error
	DeleteSMB(context.Context, int64) error
}

type SMBMountAPI interface {
	Mount(context.Context, string, string, string, string, string, string, string) error
	Remove(context.Context, string) error
}

type WindowsISOAPI interface {
	PrepareWindowsISO(context.Context, agent.WindowsISORequest) (agent.WindowsISOResult, error)
	CheckWindowsISO(context.Context, agent.WindowsISORequest) error
}

type Server struct {
	Store             Storage
	Boot              *BootClient
	Agent             *AgentClient
	Images            ImagesAPI
	SMB               SMBMountAPI
	WindowsISO        WindowsISOAPI
	AllowedInterfaces map[string]bool
	BootBaseURL       string
	menuMu            sync.Mutex
	smbMu             sync.Mutex
	windowsMu         sync.Mutex
}

type requestContext struct {
	User  User
	Token string
	CSRF  string
}
type ctxKey struct{}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.protect("view", s.logout))
	mux.HandleFunc("GET /", s.protect("view", s.dashboard))
	mux.HandleFunc("GET /events", s.protect("view", s.eventsPage))
	mux.HandleFunc("GET /network", s.protect("view", s.networkPage))
	mux.HandleFunc("POST /network/preview", s.protect("network", s.networkPreview))
	mux.HandleFunc("POST /network/apply", s.protect("network", s.networkApply))
	mux.HandleFunc("GET /menu", s.protect("view", s.menuPage))
	mux.HandleFunc("POST /menu/publish", s.protect("menu", s.menuPublish))
	mux.HandleFunc("POST /menu/rollback", s.protect("menu", s.menuRollback))
	mux.HandleFunc("GET /users", s.protect("users", s.usersPage))
	mux.HandleFunc("POST /users/create", s.protect("users", s.userCreate))
	mux.HandleFunc("POST /users/change", s.protect("users", s.userChange))
	mux.HandleFunc("POST /users/reset-password", s.protect("users", s.userResetPassword))
	mux.HandleFunc("GET /audit", s.protect("users", s.auditPage))
	mux.HandleFunc("GET /images", s.protect("view", s.imagesPage))
	mux.HandleFunc("POST /images/scan", s.protect("images", s.imagesScan))
	mux.HandleFunc("POST /images/windows-profiles", s.protect("images", s.windowsProfileCreate))
	mux.HandleFunc("POST /images/windows-profiles/{id}/prepare", s.protect("images", s.windowsProfilePrepare))
	mux.HandleFunc("POST /images/smb", s.protect("smb", s.imagesSMBCreate))
	mux.HandleFunc("POST /images/smb/{id}/retry", s.protect("smb", s.imagesSMBRetry))
	mux.HandleFunc("POST /images/smb/{id}/remove", s.protect("smb", s.imagesSMBRemove))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) protect(action string, next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("pxe_session")
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		user, err := s.Store.Session(r.Context(), cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !Allowed(user.Role, action) {
			if r.Method == http.MethodPost {
				_ = s.Store.Audit(r.Context(), user.ID, "denied:"+action, "denied", "")
			}
			http.Error(w, "Доступ запрещён", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
			if err := r.ParseForm(); err != nil || !CheckCSRF(cookie.Value, r.FormValue("csrf")) {
				_ = s.Store.Audit(r.Context(), user.ID, "denied:"+action, "csrf", "")
				http.Error(w, "Недействительный токен формы", http.StatusForbidden)
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, requestContext{User: user, Token: cookie.Value, CSRF: CSRFToken(cookie.Value)})
		next(w, r.WithContext(ctx))
	}
}

func requestInfo(r *http.Request) requestContext { return r.Context().Value(ctxKey{}).(requestContext) }

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	token, err := randomToken()
	if err != nil {
		http.Error(w, "Ошибка", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "pxe_login_csrf", Value: token, Path: "/login", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	render(w, "login", pageData{CSRF: token, Message: r.URL.Query().Get("error")})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Неверная форма", 400)
		return
	}
	cookie, err := r.Cookie("pxe_login_csrf")
	if err != nil || cookie.Value == "" || !constantEqual(cookie.Value, r.FormValue("csrf")) {
		http.Error(w, "Недействительный токен формы", 403)
		return
	}
	user, token, err := s.Store.Login(r.Context(), r.FormValue("username"), r.FormValue("password"))
	if err != nil {
		http.Redirect(w, r, "/login?error=Неверное+имя+или+пароль", 303)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "pxe_session", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(sessionLifetime)})
	http.SetCookie(w, &http.Cookie{Name: "pxe_login_csrf", Path: "/login", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	_ = s.Store.Audit(r.Context(), user.ID, "login", "ok", "")
	http.Redirect(w, r, "/", 303)
}

func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	_ = s.Store.Logout(r.Context(), info.Token)
	_ = s.Store.Audit(r.Context(), info.User.ID, "logout", "ok", "")
	http.SetCookie(w, &http.Cookie{Name: "pxe_session", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/login", 303)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r)}
	if s.Boot != nil {
		v, err := s.Boot.Status(r.Context())
		if err == nil {
			data.BootStatus = "Работает · " + v.SHA256[:min(12, len(v.SHA256))]
		} else {
			data.BootStatus = "Недоступен"
		}
	} else {
		data.BootStatus = "Недоступен"
	}
	if s.Agent != nil {
		v, err := s.Agent.Status(r.Context())
		if err == nil {
			data.AgentStatus = fmt.Sprintf("Работает · %s · %s", v.Mode, v.Interface)
		} else {
			data.AgentStatus = "Недоступен"
		}
	} else {
		data.AgentStatus = "Недоступен"
	}
	if s.Boot != nil {
		v, err := s.Boot.Events(r.Context(), 10)
		if err == nil {
			data.Events = v
		} else {
			data.EventError = "Журнал временно недоступен"
		}
	}
	render(w, "dashboard", data)
}

func (s *Server) eventsPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r)}
	if s.Boot == nil {
		data.EventError = "Служба Boot недоступна"
	} else {
		v, err := s.Boot.Events(r.Context(), 100)
		if err != nil {
			data.EventError = "Журнал временно недоступен"
		} else {
			data.Events = v
		}
	}
	render(w, "events", data)
}

func (s *Server) auditPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r)}
	v, err := s.Store.AuditList(r.Context())
	if err != nil {
		data.Message = "Не удалось прочитать аудит"
	} else {
		data.Audit = v
	}
	render(w, "audit", data)
}

func (s *Server) imagesPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message")}
	profiles, err := s.Store.WindowsProfiles(r.Context())
	if err != nil {
		data.ImageError = "Не удалось прочитать профили Windows ISO"
	} else {
		data.WindowsProfiles = profiles
	}
	if s.Images == nil {
		data.ImageError = "Сервис образов недоступен"
	} else {
		catalog, err := s.Images.Catalog(r.Context())
		if err != nil {
			data.ImageError = "Сервис образов недоступен"
		} else {
			data.Storages = catalog.Storages
			names := make(map[int64]string, len(catalog.Storages))
			ready := make(map[int64]bool, len(catalog.Storages))
			smb := make(map[int64]bool, len(catalog.Storages))
			for _, storage := range catalog.Storages {
				names[storage.ID] = storage.Name
				ready[storage.ID] = storage.State == "ready"
				smb[storage.ID] = storage.Kind == "smb"
			}
			for _, image := range catalog.Images {
				unsupported := strings.Contains(strings.ToLower(path.Base(image.Path)), "arm64")
				canPrepare := image.Present && ready[image.StorageID] && smb[image.StorageID] && !unsupported
				for _, profile := range profiles {
					if profile.StorageID == image.StorageID && profile.Path == image.Path {
						canPrepare = false
						break
					}
				}
				data.ISOImages = append(data.ISOImages, imageRow{Image: image, StorageName: names[image.StorageID], CanPrepare: canPrepare, Unsupported: unsupported})
			}
		}
	}
	render(w, "images", data)
}

func (s *Server) imagesScan(w http.ResponseWriter, r *http.Request) {
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 || s.Images == nil {
		http.Error(w, "Неверное хранилище", http.StatusBadRequest)
		return
	}
	err = s.Images.Scan(r.Context(), id)
	result := "accepted"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), requestInfo(r).User.ID, "images.scan.requested", result, strconv.FormatInt(id, 10))
	if err != nil {
		http.Error(w, "Не удалось запустить сканирование", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/images?message=Сканирование+запущено", http.StatusSeeOther)
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	data := pageData{Info: requestInfo(r), Message: r.URL.Query().Get("message")}
	v, err := s.Store.Users(r.Context())
	if err != nil {
		http.Error(w, "Не удалось прочитать пользователей", 500)
		return
	}
	data.Users = v
	render(w, "users", data)
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	err := s.Store.CreateUser(r.Context(), r.FormValue("username"), r.FormValue("password"), r.FormValue("role"))
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.create", result, r.FormValue("username"))
	if err != nil {
		http.Error(w, "Не удалось создать пользователя: "+err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/users?message=Пользователь+создан", 303)
}

func (s *Server) userChange(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	err = s.Store.ChangeUser(r.Context(), id, info.User.ID, r.FormValue("role"), r.FormValue("enabled") == "on")
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.change", result, fmt.Sprintf("id=%d", id))
	if err != nil {
		http.Error(w, "Не удалось изменить пользователя: "+err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/users?message=Пользователь+изменён", 303)
}

func (s *Server) userResetPassword(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	err = s.Store.ResetPassword(r.Context(), id, r.FormValue("password"))
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.reset-password", result, fmt.Sprintf("id=%d", id))
	if err != nil {
		http.Error(w, "Не удалось сменить пароль: "+err.Error(), 400)
		return
	}
	if id == info.User.ID {
		http.SetCookie(w, &http.Cookie{Name: "pxe_session", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		http.Redirect(w, r, "/login", 303)
		return
	}
	http.Redirect(w, r, "/users?message=Пароль+изменён", 303)
}
