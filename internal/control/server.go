package control

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"omb/internal/agent"
	"omb/internal/drivers"
	"omb/internal/images"
	"omb/internal/ueficausb"
)

type Storage interface {
	SaveAnswerCheck(context.Context, AnswerCheck) error
	LatestAnswerChecks(context.Context) ([]AnswerCheck, error)
	LatestAnswerChecksByProfile(context.Context) ([]AnswerCheck, error)
	AnswerCheckHistory(context.Context, string, int64, int64) ([]AnswerCheck, error)
	SetUserLanguage(context.Context, int64, string) error
	SetUserTheme(context.Context, int64, string) error
	ChangeOwnPassword(context.Context, int64, string, string, string) error
	Login(context.Context, string, string) (User, string, error)
	Session(context.Context, string) (User, error)
	Logout(context.Context, string) error
	Audit(context.Context, int64, string, string, string) error
	AuditList(context.Context) ([]AuditEvent, error)
	AuditPaged(context.Context, int, int64) ([]AuditEvent, bool, error)
	Users(context.Context) ([]User, error)
	CreateUser(context.Context, string, string, string) error
	CreateUserWithPolicy(context.Context, string, string, string, string, int, bool) error
	ChangeUser(context.Context, int64, int64, string, bool) error
	DeleteUser(context.Context, int64, int64) (string, error)
	ResetPassword(context.Context, int64, string) error
	ResetPasswordWithOptions(context.Context, int64, string, bool) error
	PasswordPolicy(context.Context) (PasswordPolicy, error)
	SetPasswordPolicy(context.Context, PasswordPolicy) error
	SetUserPasswordPolicy(context.Context, int64, int64, string, int, *bool) error
	MenuVersions(context.Context) ([]MenuVersion, error)
	MenuByID(context.Context, int64) (MenuVersion, error)
	SaveActiveMenu(context.Context, int64, MenuVersion) error
	MenuDraft(context.Context) (MenuDraft, bool, error)
	SaveMenuDraft(context.Context, MenuDraft) error
	BootProfiles(context.Context) ([]BootProfile, error)
	Installations(context.Context) ([]Installation, error)
	CreateInstallation(context.Context, Installation) (Installation, error)
	UpdateInstallation(context.Context, Installation) error
	DeleteInstallation(context.Context, int64) error
	CreateUbuntuAutoinstall(context.Context, UbuntuAutoinstallFile) (UbuntuAutoinstallFile, error)
	UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error)
	UbuntuAutoinstall(context.Context, int64) (UbuntuAutoinstallFile, error)
	CreateWindowsProfile(context.Context, WindowsProfileSource) (WindowsProfile, error)
	WindowsProfile(context.Context, int64) (WindowsProfile, error)
	WindowsProfiles(context.Context) ([]WindowsProfile, error)
	SetWindowsProfileResult(context.Context, int64, string, string, string) error
	SetWindowsProfileWIMInfo(context.Context, int64, agent.WindowsWIMInfo) error
	DeleteWindowsProfile(context.Context, int64) error
	CreateWindowsAnswer(context.Context, WindowsAnswerVariant) (WindowsAnswerVariant, error)
	SetWindowsAnswerState(context.Context, int64, string, string) error
	WindowsAnswers(context.Context) ([]WindowsAnswerVariant, error)
	WindowsAnswer(context.Context, int64) (WindowsAnswerVariant, error)
	DeleteWindowsAnswer(context.Context, int64) error
	SetWindowsAnswerWipesDisk(context.Context, int64, bool) error
	CreateDriverPackage(context.Context, drivers.Inventory) (bool, error)
	DeleteDriverPackage(context.Context, string) error
	DriverPackages(context.Context) ([]DriverPackage, error)
	CreateDriverConfig(context.Context, DriverConfig) (DriverConfig, error)
	DriverConfigs(context.Context) ([]DriverConfig, error)
	DriverConfig(context.Context, int64) (DriverConfig, error)
	SetDriverConfigTitle(context.Context, int64, string) error
	SetDriverConfigResult(context.Context, int64, string, agent.DriverBuildResult, string) error
	DeleteDriverConfig(context.Context, int64) error
}

type ImagesAPI interface {
	Catalog(context.Context) (images.Catalog, error)
	Folders(context.Context, int64) ([]string, error)
	UploadISO(context.Context, int64, string, string, io.Reader, int64) (images.UploadResult, error)
	Scan(context.Context, int64) error
	CreateSMB(context.Context, images.ManagedStorageInput) (images.Storage, error)
	MarkSMBError(context.Context, int64) error
	MarkSMBScanError(context.Context, int64) error
	DeleteSMB(context.Context, int64) error
	CreateNFS(context.Context, images.NFSStorageInput) (images.Storage, error)
	UpdateNFSVersion(context.Context, int64, string) error
	SetNFSUploads(context.Context, int64, bool) error
	MarkNFSError(context.Context, int64) error
	MarkNFSScanError(context.Context, int64) error
	DeleteNFS(context.Context, int64) error
	RenameStorage(context.Context, int64, string) error
	DeleteImage(context.Context, int64, string) error
	CatalogSettings(context.Context) (images.CatalogSettings, error)
	SetCatalogSettings(context.Context, images.CatalogSettings) (images.CatalogSettings, error)
	StartMigration(ctx context.Context, sourceStorageID int64, path string, targetStorageID int64) (*images.MigrationJob, error)
	ListMigrations(ctx context.Context) ([]*images.MigrationJob, error)
	GetMigration(ctx context.Context, id string) (*images.MigrationJob, error)
	CancelMigration(ctx context.Context, id string) error
	CommitDeleteSource(ctx context.Context, id string) error
}

type SMBMountAPI interface {
	Mount(context.Context, string, string, string, string, string, string, string) error
	EnableUpload(context.Context, string, string, string, string) error
	Remove(context.Context, string) error
}

type NFSMountAPI interface {
	MountNFS(ctx context.Context, key, server, exportPath, version string) (bool, error)
	EnableNFSUpload(ctx context.Context, key, server, exportPath, version string) error
	CheckNFSUpload(ctx context.Context, key string) (bool, error)
	RemoveNFS(ctx context.Context, key string) error
}

type WindowsISOAPI interface {
	PrepareWindowsISO(context.Context, agent.WindowsISORequest) (agent.WindowsISOResult, error)
	CheckWindowsISO(context.Context, agent.WindowsISORequest) (agent.WindowsISOResult, error)
	RebindWindowsISO(context.Context, agent.WindowsISORequest) (agent.WindowsISOResult, error)
}

type UbuntuISOAPI interface {
	InspectUbuntuISO(context.Context, agent.WindowsISORequest) (agent.UbuntuISOInspection, error)
	PrepareUbuntuISO(context.Context, agent.UbuntuISORequest) error
	RebindUbuntuISO(context.Context, agent.UbuntuISORequest) error
}

type WindowsAnswerAPI interface {
	PrepareWindowsAnswer(context.Context, agent.WindowsAnswerRequest) error
	CheckWindowsAnswer(context.Context, agent.WindowsAnswerCheck) error
}

type DriverPackageAPI interface {
	ImportDriverPackage(context.Context, io.Reader, int64) (drivers.Inventory, error)
	CheckDriverPackage(context.Context, string) error
}

type DriverConfigAPI interface {
	PrepareDriverConfig(context.Context, agent.DriverBuildRequest) (agent.DriverBuildResult, error)
	CheckDriverConfig(context.Context, agent.DriverBuildCheck) error
}

// UEFICAUSBAPI builds and streams the "enable the Microsoft UEFI CA key" USB stick archive.
type UEFICAUSBAPI interface {
	BuildUEFICAUSB(ctx context.Context, profileID int64, loader string) (ueficausb.Result, error)
	OpenUEFICAUSB(ctx context.Context, key string) (io.ReadCloser, int64, error)
	UEFICAUSBArchives(ctx context.Context) ([]ueficausb.Archive, error)
}

type AssetCleanupAPI interface {
	RemoveDriverPackage(context.Context, string) error
	RemoveWindowsProfile(context.Context, int64) error
	RemoveWindowsAnswer(context.Context, int64) error
	RemoveDriverConfig(context.Context, int64, string, bool) error
	DriverConfigWIMKey(context.Context, int64) (string, error)
}

type MenuReader interface {
	CurrentMenu(context.Context) ([]byte, error)
}

type DiskUsageAPI interface {
	DiskUsage(context.Context) (agent.DiskUsageReport, error)
}

type Server struct {
	BootDrivers       BootDriverSync
	driverMu          sync.Mutex
	Store             Storage
	Boot              *BootClient
	DeletionMenu      MenuReader
	Agent             *AgentClient
	SMBDisconnect     SMBSessionDisconnectAPI
	TransferCancel    BootTransferCancelAPI
	DiskUsage         DiskUsageAPI
	Images            ImagesAPI
	SMB               SMBMountAPI
	NFS               NFSMountAPI
	WindowsISO        WindowsISOAPI
	UbuntuISO         UbuntuISOAPI
	UbuntuReadiness   UbuntuReadiness
	UbuntuSource      UbuntuSourceAPI
	WindowsAnswers    WindowsAnswerAPI
	DriverPackages    DriverPackageAPI
	DriverConfigs     DriverConfigAPI
	Cleanup           AssetCleanupAPI
	StorageRecovery   StorageRecoveryAPI
	SystemTime        SystemTimeAPI
	UEFICAUSB         UEFICAUSBAPI
	AllowedInterfaces map[string]bool
	BootBaseURL       string
	SysMon            *SystemMonitor
	menuMu            sync.Mutex
	draftMu           sync.Mutex
	smbMu             sync.Mutex
	nfsMu             sync.Mutex
	migrationMu       sync.Mutex
	bookCache         machineBookCache
	machineImports    *machineImportStash
	windowsMu         sync.Mutex
	ubuntuCacheMu     sync.Mutex
	ubuntuCache       *ubuntuReadinessCache
	authorsCache      resourceAuthorsCache
}

type requestContext struct {
	User           User
	Token          string
	CSRF           string
	Path           string
	Draft          draftSummary
	PasswordPolicy PasswordPolicy
	PasswordState  PasswordState
}
type ctxKey struct{}

func (s *Server) Handler() http.Handler {
	if s.machineImports == nil {
		s.machineImports = &machineImportStash{}
	}
	if s.SysMon == nil {
		s.SysMon = NewSystemMonitor(nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /answers/{kind}/{id}/checks", s.protect("view", s.answerCheckHistory))
	mux.HandleFunc("POST /answers/{kind}/{id}/checks", s.protect("images", s.answerCheckRecord))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.protect("view", s.logout))
	mux.HandleFunc("POST /settings/language", s.protect("view", s.setLanguage))
	mux.HandleFunc("POST /settings/theme", s.protect("view", s.setTheme))
	mux.HandleFunc("GET /account", s.protect("view", s.accountPage))
	mux.HandleFunc("POST /account/password", s.protect("view", withPageErrors(s.accountPage, s.accountPassword)))
	mux.HandleFunc("GET /change-password", s.protect("view", s.changePasswordPage))
	mux.HandleFunc("POST /change-password", s.protect("view", withPageErrors(s.changePasswordPage, s.changePasswordSubmit)))
	mux.HandleFunc("GET /assets/password.js", s.passwordScript)
	mux.HandleFunc("GET /assets/login.js", loginScript)
	mux.HandleFunc("GET /assets/infinite-scroll.js", infiniteScrollScript)
	mux.HandleFunc("GET /assets/async-block.js", asyncBlockScript)
	mux.HandleFunc("GET /assets/account.js", accountScript)
	mux.HandleFunc("GET /assets/sha-chip.js", shaChipScript)
	mux.HandleFunc("GET /assets/notice.js", noticeScript)
	mux.HandleFunc("GET /assets/style.css", stylesheet)
	mux.HandleFunc("GET /assets/fonts/{name}", fontFile)
	mux.HandleFunc("GET /favicon.ico", brandFile)
	mux.HandleFunc("GET /assets/favicon.svg", brandFile)
	mux.HandleFunc("GET /assets/apple-touch-icon.png", brandFile)
	mux.HandleFunc("GET /{$}", s.protect("view", s.nowPage))
	mux.HandleFunc("GET /installations", s.protect("view", s.installationsPage))
	mux.HandleFunc("GET /assets/download-search.js", downloadSearchScript)
	mux.HandleFunc("GET /assets/now.js", nowScript)
	mux.HandleFunc("GET /assets/network.js", networkScript)
	mux.HandleFunc("GET /assets/users.js", usersScript)
	mux.HandleFunc("GET /assets/storages.js", storagesScript)
	mux.HandleFunc("GET /assets/answers.js", answersScript)
	mux.HandleFunc("GET /assets/events.js", eventsScript)
	mux.HandleFunc("GET /assets/row-menu.js", rowMenuScript)
	mux.HandleFunc("GET /assets/system.js", systemScript)
	mux.HandleFunc("GET /search", s.protect("view", s.searchPage))
	mux.HandleFunc("GET /assets/search.js", searchScript)
	mux.HandleFunc("GET /installations/new", s.protect("images", s.installationNewPage))
	mux.HandleFunc("POST /installations", s.protect("images", withPageErrors(s.installationNewPage, s.installationCreate)))
	mux.HandleFunc("GET /installations/{id}/edit", s.protect("images", s.installationNewPage))
	mux.HandleFunc("POST /installations/{id}", s.protect("images", withPageErrors(s.installationNewPage, s.installationCreate)))
	mux.HandleFunc("POST /installations/{id}/prepare", s.protect("images", withPageErrors(s.installationsPage, s.installationPrepare)))
	mux.HandleFunc("POST /installations/{id}/delete", s.protect("delete", withPageErrors(s.installationsPage, s.installationDelete)))
	mux.HandleFunc("POST /installations/{id}/remove-delete", s.protect("delete", withPageErrors(s.installationsPage, s.installationRemoveDelete)))
	mux.HandleFunc("GET /assets/installation-form.js", installationFormScript)
	mux.HandleFunc("POST /installations/{key}/add", s.protect("menu", withPageErrors(s.installationsPage, s.installationAdd)))
	mux.HandleFunc("GET /events", s.protect("view", s.eventsPage))
	mux.HandleFunc("GET /events/content", s.protect("view", s.eventsContent))
	mux.HandleFunc("GET /events/services", s.protect("view", s.eventsServices))
	mux.HandleFunc("GET /events/journal", s.protect("delete", s.eventsJournal))
	mux.HandleFunc("GET /events/machines/{mac}", s.protect("view", s.machinePage))
	mux.HandleFunc("POST /events/machines/{mac}/network-driver", s.protect("images", withPageErrors(s.machinePage, s.machineNetworkDriver)))
	mux.HandleFunc("POST /events/machines/{mac}/dhcp-ignore", s.protect("network", withPageErrors(s.machinePage, s.machineDHCPIgnore)))
	mux.HandleFunc("POST /events/machines/{mac}/edit", s.protect("images", withPageErrors(s.machinePage, s.machineEdit)))
	mux.HandleFunc("POST /events/machines/{mac}/delete", s.protect("delete", withPageErrors(s.machinePage, s.machineDelete)))
	mux.HandleFunc("POST /sessions/smb/disconnect", s.protect("images", withPageErrors(s.nowPage, s.disconnectSMBSession)))
	mux.HandleFunc("POST /sessions/http/cancel", s.protect("images", withPageErrors(s.nowPage, s.cancelHTTPTransfer)))
	mux.HandleFunc("POST /events/retention", s.protect("delete", withPageErrors(s.eventsPage, s.journalRetention)))
	mux.HandleFunc("POST /events/clear", s.protect("delete", withPageErrors(s.eventsPage, s.journalClear)))
	mux.HandleFunc("GET /machines", s.protect("view", s.machinesPage))
	mux.HandleFunc("POST /machines", s.protect("images", withPageErrors(s.machinesPage, s.machineAdd)))
	mux.HandleFunc("POST /machines/{mac}", s.protect("images", withPageErrors(s.machinesPage, s.machineTableEdit)))
	mux.HandleFunc("POST /machines/{mac}/delete", s.protect("delete", withPageErrors(s.machinesPage, s.machineTableDelete)))
	mux.HandleFunc("GET /assets/machines.js", machinesScript)
	mux.HandleFunc("GET /assets/machine-page.js", machinePageScript)
	mux.HandleFunc("GET /machines.csv", s.protect("view", s.machinesCSV))
	mux.HandleFunc("POST /machine-rules", s.protect("images", withPageErrors(s.machinesPage, s.macRuleAdd)))
	mux.HandleFunc("POST /machine-rules/{prefix}", s.protect("images", withPageErrors(s.machinesPage, s.macRuleEdit)))
	mux.HandleFunc("POST /machine-rules/{prefix}/delete", s.protect("delete", withPageErrors(s.machinesPage, s.macRuleDelete)))
	mux.HandleFunc("POST /machines/import", s.protect("images", withPageErrors(s.machinesPage, s.machineImportPreview)))
	mux.HandleFunc("POST /machines/import-apply", s.protect("images", withPageErrors(s.machinesPage, s.machineImportApply)))
	mux.HandleFunc("GET /network", s.protect("view", s.networkPage))
	mux.HandleFunc("POST /network/preview", s.protect("network", s.networkPreview))
	mux.HandleFunc("POST /network/apply", s.protect("network", s.networkApply))
	mux.HandleFunc("GET /menu", s.protect("view", s.menuPage))
	mux.HandleFunc("GET /menu/versions", s.protect("view", s.menuVersions))
	mux.HandleFunc("POST /menu/publish", s.protect("menu", withPageErrors(s.menuPage, s.menuPublish)))
	mux.HandleFunc("POST /menu/rollback", s.protect("menu", withPageErrors(s.menuPage, s.menuRollback)))
	mux.HandleFunc("POST /menu/local-timer", s.protect("menu", withPageErrors(s.menuPage, s.menuLocalTimer)))
	mux.HandleFunc("POST /menu/items/{key}/default", s.protect("menu", withPageErrors(s.menuPage, s.menuSetDefault)))
	mux.HandleFunc("POST /menu/diagnostics", s.protect("menu", withPageErrors(s.menuPage, s.menuDiagnostics)))
	mux.HandleFunc("POST /menu/discard", s.protect("menu", withPageErrors(s.menuPage, s.menuDiscard)))
	mux.HandleFunc("POST /menu/items/{key}/restore", s.protect("menu", withPageErrors(s.menuPage, s.installationAdd)))
	mux.HandleFunc("POST /menu/order", s.protect("menu", withPageErrors(s.menuPage, s.menuOrder)))
	mux.HandleFunc("GET /assets/menu-order.js", menuOrderScript)
	mux.HandleFunc("POST /menu/items/{key}/move", s.protect("menu", withPageErrors(s.menuPage, s.menuItemMove)))
	mux.HandleFunc("POST /menu/items/{key}/remove", s.protect("menu", withPageErrors(s.menuPage, s.menuItemRemove)))
	mux.HandleFunc("POST /menu/items/{key}/title", s.protect("menu", withPageErrors(s.menuPage, s.menuItemRename)))
	mux.HandleFunc("GET /users", s.protect("users", s.usersPage))
	mux.HandleFunc("POST /users/create", s.protect("users", s.userCreate))
	mux.HandleFunc("POST /users/change", s.protect("users", withPageErrors(s.usersPage, s.userChange)))
	mux.HandleFunc("POST /users/delete", s.protect("users", withPageErrors(s.usersPage, s.userDelete)))
	mux.HandleFunc("POST /users/reset-password", s.protect("users", s.userResetPassword))
	mux.HandleFunc("POST /users/password-policy", s.protect("users", withPageErrors(s.usersPage, s.usersPasswordPolicy)))
	mux.HandleFunc("POST /users/user-password-policy", s.protect("users", withPageErrors(s.usersPage, s.userPasswordPolicy)))
	mux.HandleFunc("GET /audit", s.protect("users", s.auditPage))
	mux.HandleFunc("GET /images", s.protect("view", s.imagesPage))
	mux.HandleFunc("GET /usb", s.protect("view", s.usbPage))
	mux.HandleFunc("POST /usb/uefica", s.protect("view", withPageErrors(s.usbPage, s.ueficaUSBDownload)))
	mux.HandleFunc("GET /assets/usb.js", usbScript)
	mux.HandleFunc("GET /ubuntu-autoinstall/new", s.protect("images", s.ubuntuAutoinstallNew))
	mux.HandleFunc("POST /ubuntu-autoinstall/preview", s.protect("images", s.ubuntuAutoinstallPreview))
	mux.HandleFunc("POST /ubuntu-autoinstall/preview/download", s.protect("images", s.ubuntuAutoinstallPreviewDownload))
	mux.HandleFunc("POST /ubuntu-autoinstall", s.protect("images", s.ubuntuAutoinstallSave))
	mux.HandleFunc("GET /ubuntu-autoinstall/{id}", s.protect("view", s.ubuntuAutoinstallFile))
	mux.HandleFunc("GET /ubuntu-autoinstall/{id}/download", s.protect("view", s.ubuntuAutoinstallFile))
	mux.HandleFunc("GET /ubuntu-autoinstall/{id}/copy", s.protect("images", s.ubuntuAutoinstallFile))
	mux.HandleFunc("GET /answers", s.protect("view", s.answersPage))
	mux.HandleFunc("GET /storages", s.protect("view", s.storagesPage))
	mux.HandleFunc("GET /system", s.protect("view", s.systemPage))
	mux.HandleFunc("POST /system/time", s.protect("network", withPageErrors(s.systemPage, s.systemTimeUpdate)))
	mux.HandleFunc("GET /api/system/metrics", s.protect("view", s.systemMetricsAPI))
	mux.HandleFunc("POST /answers/{id}/inspect", s.protect("images", withPageErrors(s.answersPage, s.windowsAnswerInspect)))
	mux.HandleFunc("GET /answers/editor", s.protect("view", s.answerEditorNew))
	mux.HandleFunc("GET /answers/{id}/edit", s.protect("view", s.answerEditorEdit))
	mux.HandleFunc("POST /answers/editor", s.protect("view", s.answerEditorPost))
	mux.HandleFunc("POST /answers/editor/save", s.protect("images", s.answerEditorSave))
	mux.HandleFunc("GET /assets/answer_editor.js", answerEditorScript)
	mux.HandleFunc("GET /assets/ubuntu_autoinstall_editor.js", ubuntuAutoinstallEditorScript)
	mux.HandleFunc("POST /answers/upload", s.protect("images", withPageErrors(s.answersPage, s.windowsAnswerUpload)))
	mux.HandleFunc("GET /drivers", s.protect("view", s.driversPage))
	mux.HandleFunc("POST /drivers/packages/{sha}/delete", s.protect("delete", withPageErrors(s.driversPage, s.driverPackageDelete)))
	mux.HandleFunc("POST /drivers/import", s.protect("images", s.driversImport))
	mux.HandleFunc("POST /drivers/configs/{id}/title", s.protect("images", withPageErrors(s.driversPage, s.driverConfigRename)))
	mux.HandleFunc("POST /drivers/configs/{id}/delete", s.protect("delete", withPageErrors(s.driversPage, s.driverConfigDelete)))
	mux.HandleFunc("GET /assets/driver-upload.js", s.driverUploadScript)
	mux.HandleFunc("GET /images/storages/{id}/folders", s.protect("view", s.imagesFolders))
	mux.HandleFunc("POST /images/upload/{id}", s.protect("images", s.imagesUpload))
	mux.HandleFunc("GET /assets/iso-upload.js", s.isoUploadScript)
	mux.HandleFunc("GET /assets/image-delete.js", imageDeleteScript)
	mux.HandleFunc("GET /assets/image-migrate.js", imageMigrateScript)
	mux.HandleFunc("POST /images/scan", s.protect("images", withPageErrors(s.storagesPage, s.imagesScan)))
	mux.HandleFunc("POST /images/delete", s.protect("delete", withPageErrors(s.imagesPage, s.imageDelete)))
	mux.HandleFunc("POST /images/migrate", s.protect("delete", withPageErrors(s.imagesPage, s.imageMigrate)))
	mux.HandleFunc("GET /api/images/migrations", s.protect("view", s.imageMigrationsAPI))
	mux.HandleFunc("POST /images/migrate/{id}/cancel", s.protect("delete", s.imageMigrateCancel))
	mux.HandleFunc("GET /assets/iso-check.js", isoCheckScript)
	mux.HandleFunc("GET /assets/image-details.js", imageDetailsScript)
	mux.HandleFunc("POST /images/name", s.protect("images", withPageErrors(s.imagesPage, s.imageNameSave)))
	mux.HandleFunc("POST /images/iso/inspect", s.protect("images", withPageErrors(s.imagesPage, s.inspectISO)))
	mux.HandleFunc("POST /images/windows-profiles/{id}/delete", s.protect("delete", withPageErrors(s.imagesPage, s.windowsProfileDelete)))
	mux.HandleFunc("POST /images/windows-answers/{id}/delete", s.protect("delete", withPageErrors(s.answersPage, s.windowsAnswerDelete)))
	mux.HandleFunc("POST /images/smb", s.protect("smb", withPageErrors(s.storagesPage, s.imagesSMBCreate)))
	mux.HandleFunc("POST /images/smb/{id}/retry", s.protect("smb", withPageErrors(s.storagesPage, s.imagesSMBRetry)))
	mux.HandleFunc("POST /images/storage-recovery", s.protect("smb", withPageErrors(s.storagesPage, s.storageRecoverySettings)))
	mux.HandleFunc("POST /images/storage-recovery/run", s.protect("smb", withPageErrors(s.storagesPage, s.storageRecoveryRun)))
	mux.HandleFunc("POST /images/smb/{id}/remove", s.protect("smb", withPageErrors(s.storagesPage, s.imagesSMBRemove)))
	mux.HandleFunc("POST /images/nfs", s.protect("nfs", withPageErrors(s.storagesPage, s.imagesNFSCreate)))
	mux.HandleFunc("POST /images/nfs/{id}/retry", s.protect("nfs", withPageErrors(s.storagesPage, s.imagesNFSRetry)))
	mux.HandleFunc("POST /images/nfs/{id}/remove", s.protect("nfs", withPageErrors(s.storagesPage, s.imagesNFSRemove)))
	mux.HandleFunc("POST /images/storages/{id}/rename", s.protect("smb", withPageErrors(s.storagesPage, s.storageRename)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) protect(action string, next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("omb_session")
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
			http.Error(w, translate(normalizeLanguage(user.Language), "Доступ запрещён"), http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			var err error
			if r.Pattern == "POST /images/upload/{id}" {
				if r.ContentLength <= 0 || r.ContentLength > images.MaxISOBytes {
					http.Error(w, translate(normalizeLanguage(user.Language), "Недопустимый размер ISO"), http.StatusBadRequest)
					return
				}
				if !CheckCSRF(cookie.Value, r.Header.Get("X-CSRF-Token")) {
					_ = s.Store.Audit(r.Context(), user.ID, "denied:"+action, "csrf", "")
					http.Error(w, translate(normalizeLanguage(user.Language), "Недействительный токен формы"), http.StatusForbidden)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, images.MaxISOBytes)
			} else if strings.HasPrefix(r.Pattern, "POST /ubuntu-autoinstall") {
				if r.ContentLength > 512<<10 {
					http.Error(w, "Форма больше 512 КиБ", http.StatusRequestEntityTooLarge)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
				err = r.ParseForm()
				var sizeError *http.MaxBytesError
				if errors.As(err, &sizeError) {
					http.Error(w, "Форма больше 512 КиБ", http.StatusRequestEntityTooLarge)
					return
				}
			} else if r.Pattern == "POST /drivers/import" {
				_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
				r.Body = http.MaxBytesReader(w, r.Body, drivers.MaxArchiveBytes+(1<<20))
				err = r.ParseMultipartForm(8 << 20)
			} else if r.Pattern == "POST /machines/import" {
				if r.ContentLength > maxMachineCSVBytes+(64<<10) {
					http.Error(w, translate(normalizeLanguage(user.Language), "Файл больше 1 МБ"), http.StatusRequestEntityTooLarge)
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, maxMachineCSVBytes+(64<<10))
				err = r.ParseMultipartForm(2 << 20)
			} else if r.Pattern == "POST /answers/{kind}/{id}/checks" {
				// 2200 Unicode characters may need 26400 bytes in percent-encoded UTF-8.
				r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
				err = r.ParseForm()
				var sizeError *http.MaxBytesError
				if errors.As(err, &sizeError) {
					http.Error(w, translate(normalizeLanguage(user.Language), "Форма больше 32 КиБ"), http.StatusRequestEntityTooLarge)
					return
				}
			} else if r.Pattern == "POST /answers/editor" || r.Pattern == "POST /answers/editor/save" {
				// The editor posts all its settings, kept XML included.
				r.Body = http.MaxBytesReader(w, r.Body, 2*maxWindowsAnswerBytes+(64<<10))
				err = r.ParseForm()
			} else if r.Pattern == "POST /answers/upload" || r.Pattern == "POST /answers/{id}/inspect" {
				r.Body = http.MaxBytesReader(w, r.Body, maxWindowsAnswerBytes+(32<<10))
				err = r.ParseMultipartForm(2 << 20)
			} else if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
				err = r.ParseMultipartForm(64 << 10)
			} else {
				r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
				err = r.ParseForm()
			}
			csrf := r.FormValue("csrf")
			if csrf == "" {
				csrf = r.Header.Get("X-CSRF-Token")
			}
			if r.Pattern != "POST /images/upload/{id}" && (err != nil || !CheckCSRF(cookie.Value, csrf)) {
				_ = s.Store.Audit(r.Context(), user.ID, "denied:"+action, "csrf", "")
				msg := translate(normalizeLanguage(user.Language), "Недействительный токен формы")
				if strings.Contains(r.Header.Get("Accept"), "application/json") || r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusForbidden)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
					return
				}
				http.Error(w, msg, http.StatusForbidden)
				return
			}
		}
		info := requestContext{User: user, Token: cookie.Value, CSRF: CSRFToken(cookie.Value), Path: r.URL.Path}
		policy, err := s.Store.PasswordPolicy(r.Context())
		if err != nil {
			http.Error(w, translate(normalizeLanguage(user.Language), "Не удалось проверить политику паролей"), http.StatusServiceUnavailable)
			return
		}
		pwState := EvaluatePasswordState(user, policy, time.Now())
		info.PasswordPolicy = policy
		info.PasswordState = pwState
		if pwState.RequiresChange() && !isForcedPasswordAllowed(r) {
			if r.Method == http.MethodGet && !isForcedPasswordAPIRequest(r) {
				http.Redirect(w, r, "/change-password", http.StatusSeeOther)
				return
			}
			msg := translate(normalizeLanguage(user.Language), "Требуется смена пароля")
			if isForcedPasswordAPIRequest(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
				return
			}
			http.Error(w, msg, http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet && needsDraftSummary(r) && !pwState.RequiresChange() {
			info.Draft = s.draftStatus(r.Context())
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, info)
		translateErrors(normalizeLanguage(user.Language), next)(w, r.WithContext(ctx))
	}
}

func isForcedPasswordAllowed(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /change-password", "POST /change-password", "POST /logout", "POST /settings/language", "POST /settings/theme":
		return true
	default:
		return false
	}
}

func isForcedPasswordAPIRequest(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/") ||
		r.URL.Query().Get("format") == "json" ||
		strings.Contains(r.Header.Get("Accept"), "application/json") ||
		r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

func requestInfo(r *http.Request) requestContext { return r.Context().Value(ctxKey{}).(requestContext) }

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	token, err := randomToken()
	if err != nil {
		http.Error(w, "Ошибка", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "omb_login_csrf", Value: token, Path: "/login", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	render(w, "login", pageData{Lang: languageFromRequest(r), CSRF: token, Message: r.URL.Query().Get("error")})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Неверная форма", 400)
		return
	}
	cookie, err := r.Cookie("omb_login_csrf")
	if err != nil || cookie.Value == "" || !constantEqual(cookie.Value, r.FormValue("csrf")) {
		http.Error(w, translate(languageFromRequest(r), "Недействительный токен формы"), 403)
		return
	}
	user, token, err := s.Store.Login(r.Context(), r.FormValue("username"), r.FormValue("password"))
	if err != nil {
		http.Redirect(w, r, "/login?error=Неверное+имя+или+пароль", 303)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "omb_session", Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(sessionLifetime)})
	http.SetCookie(w, &http.Cookie{Name: "omb_login_csrf", Path: "/login", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	_ = s.Store.Audit(r.Context(), user.ID, "login", "ok", "")
	if policy, err := s.Store.PasswordPolicy(r.Context()); err == nil {
		if EvaluatePasswordState(user, policy, time.Now()).RequiresChange() {
			http.Redirect(w, r, "/change-password", 303)
			return
		}
	}
	http.Redirect(w, r, "/", 303)
}

func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	_ = s.Store.Logout(r.Context(), info.Token)
	_ = s.Store.Audit(r.Context(), info.User.ID, "logout", "ok", "")
	http.SetCookie(w, &http.Cookie{Name: "omb_session", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.Redirect(w, r, "/login", 303)
}

// loadServiceStatus fills the Boot and Network Agent status lines.
func (s *Server) loadServiceStatus(ctx context.Context, data *pageData) {
	if s.Boot != nil {
		v, err := s.Boot.Status(ctx)
		if err == nil {
			data.BootStatus = "Работает · " + v.SHA256[:min(12, len(v.SHA256))]
			data.BootOK = true
		} else {
			data.BootStatus = "Недоступен"
		}
	} else {
		data.BootStatus = "Недоступен"
	}
	if s.Agent != nil {
		v, err := s.Agent.Status(ctx)
		if err == nil {
			data.AgentStatus = fmt.Sprintf("Работает · %s · %s", v.Mode, v.Interface)
			data.AgentOK = true
		} else {
			data.AgentStatus = "Недоступен"
		}
	} else {
		data.AgentStatus = "Недоступен"
	}
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
	render(w, "images", s.imagesLibraryData(r))
}

func (s *Server) answersPage(w http.ResponseWriter, r *http.Request) {
	render(w, "answers", s.answersLibraryData(r))
}

func (s *Server) storagesPage(w http.ResponseWriter, r *http.Request) {
	data := s.storageLibraryData(r)
	data.Sort = r.URL.Query().Get("sort")
	data.Dir = r.URL.Query().Get("dir")
	if data.Sort == "" {
		data.Sort = "size"
		data.Dir = "desc"
	}
	s.loadStorageSpace(r.Context(), &data, data.catalogCache)
	if data.StorageSpace != nil && len(data.StorageSpace.Artifacts) > 0 {
		sortServerArtifacts(data.StorageSpace.Artifacts, data.Sort, data.Dir)
	}
	render(w, "storages", data)
}

func (s *Server) imagesScan(w http.ResponseWriter, r *http.Request) {
	s.smbMu.Lock()
	defer s.smbMu.Unlock()
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 || s.Images == nil {
		http.Error(w, "Неверное хранилище", http.StatusBadRequest)
		return
	}
	var storageName string
	if catalog, err := s.Images.Catalog(r.Context()); err == nil {
		for _, st := range catalog.Storages {
			if st.ID == id {
				storageName = st.Name
				if s.NFS != nil && st.Kind == "nfs" {
					if writable, err := s.NFS.CheckNFSUpload(r.Context(), st.Key); err == nil {
						_ = s.Images.SetNFSUploads(r.Context(), st.ID, writable)
					}
				}
				break
			}
		}
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
	msg := "Сканирование запущено"
	if storageName != "" {
		msg = fmt.Sprintf("Сканирование хранилища «%s» запущено", storageName)
	}
	http.Redirect(w, r, "/storages?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) usersPage(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	data := pageData{Info: info, Message: r.URL.Query().Get("message"), Error: pageError(r), PasswordPolicy: info.PasswordPolicy, NowTime: time.Now()}
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
	mode := NormalizePasswordMode(r.FormValue("password_policy_mode"))
	customDays := 0
	if mode == PasswordModeCustom {
		n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("password_max_age_days")))
		if err != nil {
			http.Error(w, "Некорректный срок действия пароля", http.StatusBadRequest)
			return
		}
		customDays = n
	}
	mustChange := r.FormValue("must_change_password") == "on"
	err := s.Store.CreateUserWithPolicy(r.Context(), r.FormValue("username"), r.FormValue("password"), r.FormValue("role"), mode, customDays, mustChange)
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.create", result, r.FormValue("username"))
	if err != nil {
		http.Error(w, "Не удалось создать пользователя: "+err.Error(), 400)
		return
	}
	msg := fmt.Sprintf("Пользователь «%s» создан", r.FormValue("username"))
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), 303)
}

func (s *Server) usersPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	maxAge, err1 := strconv.Atoi(strings.TrimSpace(r.FormValue("max_age_days")))
	rawWarn := strings.TrimSpace(r.FormValue("warn_days"))
	var warnDays int
	var err2 error
	if rawWarn == "" && err1 == nil && maxAge == 0 {
		warnDays = info.PasswordPolicy.WarnDays
	} else {
		warnDays, err2 = strconv.Atoi(rawWarn)
	}
	if err1 != nil || err2 != nil {
		http.Error(w, "Укажите целые числа для срока действия и предупреждения", http.StatusBadRequest)
		return
	}
	policy := PasswordPolicy{MaxAgeDays: maxAge, WarnDays: warnDays}
	if err := ValidatePasswordPolicy(policy); err != nil {
		http.Error(w, "Недопустимые значения политики паролей (срок 0–3650 дн., предупреждение 0–90 дн.)", http.StatusBadRequest)
		return
	}
	err := s.Store.SetPasswordPolicy(r.Context(), policy)
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "users.password-policy", result, fmt.Sprintf("max_age_days=%d warn_days=%d", maxAge, warnDays))
	if err != nil {
		http.Error(w, "Не удалось сохранить политику паролей", http.StatusServiceUnavailable)
		return
	}
	var msg string
	if maxAge == 0 {
		msg = "Политика паролей обновлена: бессрочно"
	} else {
		msg = fmt.Sprintf("Политика паролей обновлена: %d дн.", maxAge)
	}
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) userPasswordPolicy(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный ID", http.StatusBadRequest)
		return
	}
	mode := r.FormValue("password_policy_mode")
	customDays := 0
	if mode == PasswordModeCustom {
		n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("password_max_age_days")))
		if err != nil {
			http.Error(w, "Некорректный срок действия пароля", http.StatusBadRequest)
			return
		}
		customDays = n
	}
	if err := ValidateUserPasswordRule(mode, customDays); err != nil {
		http.Error(w, "Укажите корректный режим и срок действия пароля (1–3650 дн.)", http.StatusBadRequest)
		return
	}
	var mustChangePtr *bool
	if id != info.User.ID {
		mc := r.FormValue("must_change_password") == "on"
		mustChangePtr = &mc
	}
	err = s.Store.SetUserPasswordPolicy(r.Context(), id, info.User.ID, mode, customDays, mustChangePtr)
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.password-policy", result, fmt.Sprintf("id=%d mode=%s days=%d", id, mode, customDays))
	switch {
	case errors.Is(err, errUserNotFound):
		http.Error(w, "Пользователь не найден", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Не удалось обновить настройки пароля", http.StatusBadRequest)
		return
	}
	username := s.lookupUsername(r.Context(), id)
	msg := "Настройки срока пароля обновлены"
	if username != "" {
		msg = fmt.Sprintf("Настройки срока пароля пользователя «%s» обновлены", username)
	}
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) lookupUsername(ctx context.Context, id int64) string {
	users, err := s.Store.Users(ctx)
	if err != nil {
		return ""
	}
	for _, u := range users {
		if u.ID == id {
			return u.Username
		}
	}
	return ""
}

func (s *Server) userChange(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	role := r.FormValue("role")
	enabled := r.FormValue("enabled") == "on"
	if id == info.User.ID && (!enabled || role != "admin") {
		_ = s.Store.Audit(r.Context(), info.User.ID, "user.change", "error", fmt.Sprintf("id=%d self", id))
		http.Error(w, "Нельзя отключить свою учётную запись или понизить свою роль администратора", 400)
		return
	}
	err = s.Store.ChangeUser(r.Context(), id, info.User.ID, role, enabled)
	result := "ok"
	if err != nil {
		result = "error"
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.change", result, fmt.Sprintf("id=%d", id))
	switch {
	case errors.Is(err, errSelfDemote):
		http.Error(w, "Нельзя отключить свою учётную запись или понизить свою роль администратора", 400)
		return
	case errors.Is(err, errLastAdmin):
		http.Error(w, "Нельзя отключить или понизить последнего администратора", 400)
		return
	case err != nil:
		http.Error(w, "Не удалось изменить пользователя: "+err.Error(), 400)
		return
	}
	var username string
	if users, err := s.Store.Users(r.Context()); err == nil {
		for _, u := range users {
			if u.ID == id {
				username = u.Username
				break
			}
		}
	}
	msg := "Пользователь изменён"
	if username != "" {
		msg = fmt.Sprintf("Пользователь «%s» обновлён", username)
	}
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), 303)
}

func (s *Server) userDelete(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный ID", http.StatusBadRequest)
		return
	}
	if id == info.User.ID {
		_ = s.Store.Audit(r.Context(), info.User.ID, "user.delete", "error", fmt.Sprintf("id=%d self", id))
		http.Error(w, "Нельзя удалить свою учётную запись", http.StatusBadRequest)
		return
	}
	username, err := s.Store.DeleteUser(r.Context(), id, info.User.ID)
	result := "ok"
	detail := fmt.Sprintf("id=%d", id)
	if err != nil {
		result = "error"
	} else if username != "" {
		detail = fmt.Sprintf("id=%d username=%s", id, username)
	}
	_ = s.Store.Audit(r.Context(), info.User.ID, "user.delete", result, detail)
	switch {
	case errors.Is(err, errSelfDelete):
		http.Error(w, "Нельзя удалить свою учётную запись", http.StatusBadRequest)
		return
	case errors.Is(err, errLastAdmin):
		http.Error(w, "Нельзя удалить последнего администратора", http.StatusBadRequest)
		return
	case errors.Is(err, errUserNotFound):
		http.Error(w, "Пользователь не найден", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Не удалось удалить пользователя: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.InvalidateResourceAuthorsCache()
	msg := "Пользователь удалён"
	if username != "" {
		msg = fmt.Sprintf("Пользователь «%s» удалён", username)
	}
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) userResetPassword(w http.ResponseWriter, r *http.Request) {
	info := requestInfo(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "Неверный ID", 400)
		return
	}
	mustChange := id != info.User.ID && r.FormValue("must_change_password") == "on"
	err = s.Store.ResetPasswordWithOptions(r.Context(), id, r.FormValue("password"), mustChange)
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
		http.SetCookie(w, &http.Cookie{Name: "omb_session", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		http.Redirect(w, r, "/login", 303)
		return
	}
	var username string
	if users, err := s.Store.Users(r.Context()); err == nil {
		for _, u := range users {
			if u.ID == id {
				username = u.Username
				break
			}
		}
	}
	msg := "Пароль изменён"
	if username != "" {
		msg = fmt.Sprintf("Пароль пользователя «%s» изменён", username)
	}
	http.Redirect(w, r, "/users?message="+url.QueryEscape(msg), 303)
}

// systemPage is the overview of the System section: services, storages,
// the PXE network and, for administrators, users.
func (s *Server) systemPage(w http.ResponseWriter, r *http.Request) {
	data := s.storageLibraryData(r)
	s.loadStorageSpace(r.Context(), &data, data.catalogCache)
	s.loadServiceStatus(r.Context(), &data)
	if s.SysMon != nil {
		data.Metrics = s.SysMon.Snapshot(normalizeLanguage(data.Info.User.Language))
	}
	if s.Agent != nil {
		if c, err := s.Agent.Config(r.Context()); err == nil {
			data.Network, data.HasNetwork = c, true
		}
	}
	s.loadSystemTime(r.Context(), &data)
	if Allowed(data.Info.User.Role, "users") {
		if users, err := s.Store.Users(r.Context()); err == nil {
			data.Users = users
		}
	}
	render(w, "system", data)
}

func (s *Server) systemMetricsAPI(w http.ResponseWriter, r *http.Request) {
	lang := normalizeLanguage(requestInfo(r).User.Language)
	var snap SystemSnapshot
	if s.SysMon != nil {
		snap = s.SysMon.Snapshot(lang)
	}
	if api := s.systemTimeAPI(); api != nil {
		tctx, cancel := context.WithTimeout(r.Context(), 1500*time.Millisecond)
		if st, err := api.SystemTime(tctx); err == nil {
			snap.TimeStatus = buildSystemTimeStatusSnapshot(st, lang)
			snap.ServerTime = time.Now().In(serverLocation()).Format("02.01.2006 15:04:05")
		}
		cancel()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snap)
}
