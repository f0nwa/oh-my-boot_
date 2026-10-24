package unattend

// The settings model of the generator, field for field as Configuration and
// the settings records of the original. Names are the C# names, so settings
// files read by both the port and the golden-file tool are the same JSON.
//
// A setting with several forms (ILanguageSettings and the like) is a Go
// interface; each form is a struct named as the C# class. Lookup values
// (languages, keyboards, editions, …) are their IDs.

// Enumerations, as strings with the C# member names.
type (
	RecoveryMode          string // None, Partition
	PartitionLayout       string // MBR, GPT, Automatic
	ProcessorArchitecture string // x86, amd64, arm64
	ExpressSettingsMode   string // Interactive, EnableAll, DisableAll
	TaskbarSearchMode     string // Hide, Icon, Box, Label
	HideModes             string // None, HiddenSystem, Hidden
	LockKeyInitial        string // Off, On
	LockKeyBehavior       string // Toggle, Ignore
	StickyKeys            string // HotKeyActive, Indicator, TriState, TwoKeysOff, AudibleFeedback, HotKeySound
	Effect                string // ControlAnimations, …, DropShadow
	ColorTheme            string // Dark, Light
	ScriptType            string // Cmd, Ps1, Reg, Vbs
	ScriptPhase           string // System, FirstLogon, UserOnce, DefaultUser
	WifiAuthentications   string // Open, WPA2PSK, WPA3SAE
	Pass                  string // offlineServicing, windowsPE, …, oobeSystem
)

// Config is Configuration.
type Config struct {
	LanguageSettings              LanguageSettings
	AccountSettings               AccountSettings
	LockoutSettings               LockoutSettings
	PasswordExpirationSettings    PasswordExpirationSettings
	ProcessAuditSettings          ProcessAuditSettings
	ComputerNameSettings          ComputerNameSettings
	TimeZoneSettings              TimeZoneSettings
	WifiSettings                  WifiSettings
	AppLockerSettings             AppLockerSettings
	ProcessorArchitectures        []ProcessorArchitecture
	Components                    []ComponentSetting
	Bloatwares                    []string
	ExpressSettings               ExpressSettingsMode
	ScriptSettings                ScriptSettings
	LockKeySettings               LockKeySettings
	WallpaperSettings             WallpaperSettings
	LockScreenSettings            LockScreenSettings
	ColorSettings                 ColorSettings
	PESettings                    PESettings
	ActivationKey                 *ProductKey
	BypassNetworkCheck            bool
	EnableLongPaths               bool
	EnableRemoteDesktop           bool
	HardenSystemDriveAcl          bool
	DeleteJunctions               bool
	AllowPowerShellScripts        bool
	DisableLastAccess             bool
	PreventAutomaticReboot        bool
	DisableSac                    bool
	DisableUac                    bool
	DisableSmartScreen            bool
	DisableSystemRestore          bool
	DisableFastStartup            bool
	TurnOffSystemSounds           bool
	DisableAppSuggestions         bool
	DisableWidgets                bool
	VBoxGuestAdditions            bool
	VMwareTools                   bool
	VirtIoGuestTools              bool
	ParallelsTools                bool
	PreventDeviceEncryption       bool
	ClassicContextMenu            bool
	LeftTaskbar                   bool
	HideTaskViewButton            bool
	ShowFileExtensions            bool
	ShowAllTrayIcons              bool
	HideFiles                     HideModes
	HideEdgeFre                   bool
	DisableEdgeStartupBoost       bool
	MakeEdgeUninstallable         bool
	DeleteEdgeDesktopIcon         bool
	LaunchToThisPC                bool
	DisableWindowsUpdate          bool
	DisablePointerPrecision       bool
	DeleteWindowsOld              bool
	DisableBingResults            bool
	UseConfigurationSet           bool
	HidePowerShellWindows         bool
	ShowEndTask                   bool
	KeepSensitiveFiles            bool
	UseNarrator                   bool
	DisableCoreIsolation          bool
	DisableAutomaticRestartSignOn bool
	HideInfoTip                   bool
	DisableWpbt                   bool
	PreventDeviceApps             bool
	TaskbarSearch                 TaskbarSearchMode
	StartPinsSettings             StartPinsSettings
	StartTilesSettings            StartTilesSettings
	TaskbarIcons                  TaskbarIcons
	Effects                       Effects
	DesktopIcons                  DesktopIconSettings
	StickyKeysSettings            StickyKeysSettings
	StartFolderSettings           StartFolderSettings

	// PXE Platform extensions, not in upstream. They stay empty for files
	// made in the editor and keep what an opened foreign file has beyond the
	// recognized settings.
	ExtraXML        []ExtraXML       // merged into the result by Build
	ImportedScripts []ImportedScript // run by the script sequences
}

// ExtraXML is one element of a foreign answer file kept as it was. Pass and
// Component name the component it belongs to ("" for a root element), Path
// the element inside the component it goes into ("OOBE", "" for the
// component itself).
type ExtraXML struct {
	Pass, Component, Arch, Path, XML string
}

// ImportedScript is a script sequence of a foreign file made by the
// Schneegans generator (Specialize.ps1 and the like). It is written to
// C:\Windows\Setup\Scripts\Imported\Name and run from the same phase.
type ImportedScript struct {
	Phase   ScriptPhase
	Name    string
	Content string
}

// Default is Configuration.Default.
func Default() Config {
	return Config{
		LanguageSettings:           InteractiveLanguageSettings{},
		AccountSettings:            InteractiveMicrosoftAccountSettings{},
		LockoutSettings:            DefaultLockoutSettings{},
		PasswordExpirationSettings: DefaultPasswordExpirationSettings{},
		ProcessAuditSettings:       DisabledProcessAuditSettings{},
		ComputerNameSettings:       RandomComputerNameSettings{},
		TimeZoneSettings:           ImplicitTimeZoneSettings{},
		WifiSettings:               InteractiveWifiSettings{},
		AppLockerSettings:          SkipAppLockerSettings{},
		ProcessorArchitectures:     []ProcessorArchitecture{"amd64"},
		Components:                 []ComponentSetting{},
		Bloatwares:                 []string{},
		ExpressSettings:            "DisableAll",
		ScriptSettings:             ScriptSettings{Scripts: []Script{}, RestartExplorer: false},
		LockKeySettings:            SkipLockKeySettings{},
		WallpaperSettings:          DefaultWallpaperSettings{},
		LockScreenSettings:         DefaultLockScreenSettings{},
		ColorSettings:              DefaultColorSettings{},
		PESettings:                 DefaultPESettings{EditionSettings: InteractiveEditionSettings{}, BypassRequirementsCheck: true},
		HideFiles:                  "Hidden",
		TaskbarSearch:              "Box",
		StartPinsSettings:          DefaultStartPinsSettings{},
		StartTilesSettings:         DefaultStartTilesSettings{},
		TaskbarIcons:               DefaultTaskbarIcons{},
		Effects:                    DefaultEffects{},
		DesktopIcons:               DefaultDesktopIconSettings{},
		StickyKeysSettings:         DefaultStickyKeysSettings{},
		StartFolderSettings:        DefaultStartFolderSettings{},
		ExtraXML:                   []ExtraXML{},
		ImportedScripts:            []ImportedScript{},
	}
}

// IsDefenderDisabled is Configuration.IsDefenderDisabled.
func (c Config) IsDefenderDisabled() bool {
	pe, ok := c.PESettings.(GeneratePESettings)
	return ok && pe.DisableDefender
}

// ComponentAndPass names a component in a pass.
type ComponentAndPass struct {
	Component string
	Pass      Pass
}

// ComponentSetting is one entry of Configuration.Components: raw XML for a
// component in a pass.
type ComponentSetting struct {
	Key   ComponentAndPass
	Value string
}

// ProductKey is a product key in the form XXXXX-XXXXX-XXXXX-XXXXX-XXXXX.
type ProductKey struct{ Value string }

// Language: ILanguageSettings.
type LanguageSettings interface{ isLanguageSettings() }

type InteractiveLanguageSettings struct{}

type LocaleAndKeyboard struct {
	Locale   string // UserLocale ID
	Keyboard string // KeyboardIdentifier ID
}

type UnattendedLanguageSettings struct {
	ImageLanguage      string
	LocaleAndKeyboard  LocaleAndKeyboard
	LocaleAndKeyboard2 *LocaleAndKeyboard
	LocaleAndKeyboard3 *LocaleAndKeyboard
	GeoLocation        string
}

func (InteractiveLanguageSettings) isLanguageSettings() {}
func (UnattendedLanguageSettings) isLanguageSettings()  {}

// Accounts: IAccountSettings, IAutoLogonSettings.
type AccountSettings interface{ isAccountSettings() }

type InteractiveMicrosoftAccountSettings struct{}
type InteractiveLocalAccountSettings struct{}

type Account struct {
	Name, DisplayName, Password, Group string
}

type UnattendedAccountSettings struct {
	Accounts          []Account
	AutoLogonSettings AutoLogonSettings
	ObscurePasswords  bool
}

func (InteractiveMicrosoftAccountSettings) isAccountSettings() {}
func (InteractiveLocalAccountSettings) isAccountSettings()     {}
func (UnattendedAccountSettings) isAccountSettings()           {}

type AutoLogonSettings interface{ isAutoLogonSettings() }

type NoneAutoLogonSettings struct{}
type BuiltinAutoLogonSettings struct{ Password string }
type OwnAutoLogonSettings struct{}

func (NoneAutoLogonSettings) isAutoLogonSettings()    {}
func (BuiltinAutoLogonSettings) isAutoLogonSettings() {}
func (OwnAutoLogonSettings) isAutoLogonSettings()     {}

// Lockout: ILockoutSettings.
type LockoutSettings interface{ isLockoutSettings() }

type DefaultLockoutSettings struct{}
type DisableLockoutSettings struct{}
type CustomLockoutSettings struct {
	LockoutThreshold, LockoutDuration, LockoutWindow *int
}

func (DefaultLockoutSettings) isLockoutSettings() {}
func (DisableLockoutSettings) isLockoutSettings() {}
func (CustomLockoutSettings) isLockoutSettings()  {}

// Password expiration: IPasswordExpirationSettings.
type PasswordExpirationSettings interface{ isPasswordExpirationSettings() }

type DefaultPasswordExpirationSettings struct{}
type UnlimitedPasswordExpirationSettings struct{}
type CustomPasswordExpirationSettings struct{ MaxAge *int }

func (DefaultPasswordExpirationSettings) isPasswordExpirationSettings()   {}
func (UnlimitedPasswordExpirationSettings) isPasswordExpirationSettings() {}
func (CustomPasswordExpirationSettings) isPasswordExpirationSettings()    {}

// Process audit: IProcessAuditSettings.
type ProcessAuditSettings interface{ isProcessAuditSettings() }

type DisabledProcessAuditSettings struct{}
type EnabledProcessAuditSettings struct{ IncludeCommandLine bool }

func (DisabledProcessAuditSettings) isProcessAuditSettings() {}
func (EnabledProcessAuditSettings) isProcessAuditSettings()  {}

// Computer name: IComputerNameSettings.
type ComputerNameSettings interface{ isComputerNameSettings() }

type RandomComputerNameSettings struct{}
type CustomComputerNameSettings struct{ Name string }
type ScriptComputerNameSettings struct{ Script string }
type PromptComputerNameSettings struct{}

func (RandomComputerNameSettings) isComputerNameSettings() {}
func (CustomComputerNameSettings) isComputerNameSettings() {}
func (ScriptComputerNameSettings) isComputerNameSettings() {}
func (PromptComputerNameSettings) isComputerNameSettings() {}

// Time zone: ITimeZoneSettings.
type TimeZoneSettings interface{ isTimeZoneSettings() }

type ImplicitTimeZoneSettings struct{}
type ExplicitTimeZoneSettings struct{ TimeZone string } // TimeOffset ID

func (ImplicitTimeZoneSettings) isTimeZoneSettings() {}
func (ExplicitTimeZoneSettings) isTimeZoneSettings() {}

// Wi-Fi: IWifiSettings, IProfileWifiSettings.
type WifiSettings interface{ isWifiSettings() }

type SkipWifiSettings struct{}
type InteractiveWifiSettings struct{}
type XmlWifiSettings struct{ Xml string }
type ParameterizedWifiSettings struct {
	Name                 string
	Password             string
	ConnectAutomatically bool
	Authentication       WifiAuthentications
	NonBroadcast         bool
}

func (SkipWifiSettings) isWifiSettings()          {}
func (InteractiveWifiSettings) isWifiSettings()   {}
func (XmlWifiSettings) isWifiSettings()           {}
func (ParameterizedWifiSettings) isWifiSettings() {}

// AppLocker: IAppLockerSettings.
type AppLockerSettings interface{ isAppLockerSettings() }

type SkipAppLockerSettings struct{}
type ConfigureAppLockerSettings struct{ PolicyXml string }

func (SkipAppLockerSettings) isAppLockerSettings()      {}
func (ConfigureAppLockerSettings) isAppLockerSettings() {}

// Scripts.
type Script struct {
	Content string
	Phase   ScriptPhase
	Type    ScriptType
}

type ScriptSettings struct {
	Scripts         []Script
	RestartExplorer bool
}

// Lock keys: ILockKeySettings.
type LockKeySettings interface{ isLockKeySettings() }

type SkipLockKeySettings struct{}
type LockKeySetting struct {
	Initial  LockKeyInitial
	Behavior LockKeyBehavior
}
type ConfigureLockKeySettings struct {
	CapsLock, NumLock, ScrollLock LockKeySetting
}

func (SkipLockKeySettings) isLockKeySettings()      {}
func (ConfigureLockKeySettings) isLockKeySettings() {}

// Personalization: IWallpaperSettings, ILockScreenSettings, IColorSettings.
// Colors are HTML colors, #RRGGBB.
type WallpaperSettings interface{ isWallpaperSettings() }

type DefaultWallpaperSettings struct{}
type SolidWallpaperSettings struct{ Color string }
type ScriptWallpaperSettings struct{ Script string }

func (DefaultWallpaperSettings) isWallpaperSettings() {}
func (SolidWallpaperSettings) isWallpaperSettings()   {}
func (ScriptWallpaperSettings) isWallpaperSettings()  {}

type LockScreenSettings interface{ isLockScreenSettings() }

type DefaultLockScreenSettings struct{}
type ScriptLockScreenSettings struct{ Script string }

func (DefaultLockScreenSettings) isLockScreenSettings() {}
func (ScriptLockScreenSettings) isLockScreenSettings()  {}

type ColorSettings interface{ isColorSettings() }

type DefaultColorSettings struct{}
type CustomColorSettings struct {
	SystemTheme          ColorTheme
	AppsTheme            ColorTheme
	EnableTransparency   bool
	AccentColorOnStart   bool
	AccentColorOnBorders bool
	AccentColor          string
}

func (DefaultColorSettings) isColorSettings() {}
func (CustomColorSettings) isColorSettings()  {}

// Windows PE: IPESettings, ICmdPESettings and their parts.
type PESettings interface{ isPESettings() }

type DefaultPESettings struct {
	EditionSettings         EditionSettings
	BypassRequirementsCheck bool
}
type GeneratePESettings struct {
	PartitionSettings     PartitionSettings
	InstallFromSettings   InstallFromSettings
	PagingFileSettings    PagingFileSettings
	DisableDefender       bool
	Disable8Dot3Names     bool
	PauseBeforeFormatting bool
	PauseBeforeReboot     bool
	CompactOs             bool
	SkipIntegrityCheck    bool
}
type ScriptPESetttings struct{ Script string } // the C# name has three t's

func (DefaultPESettings) isPESettings()  {}
func (GeneratePESettings) isPESettings() {}
func (ScriptPESetttings) isPESettings()  {}

type EditionSettings interface{ isEditionSettings() }

type InteractiveEditionSettings struct{}
type FirmwareEditionSettings struct{}
type UnattendedEditionSettings struct{ Edition string } // WindowsEdition ID
type CustomEditionSettings struct{ ProductKey ProductKey }

func (InteractiveEditionSettings) isEditionSettings() {}
func (FirmwareEditionSettings) isEditionSettings()    {}
func (UnattendedEditionSettings) isEditionSettings()  {}
func (CustomEditionSettings) isEditionSettings()      {}

type TargetDiskSettings interface{ isTargetDiskSettings() }

type InteractiveTargetDiskSettings struct{}
type ScriptTargetDiskSettings struct{ Script string }
type FixedTargetDiskSettings struct{ Index int }
type GeneratedTargetDiskSettings struct {
	MinSizeGiB          *int
	MaxSizeGiB          *int
	Index               *int
	AssertNoPartitions  bool
	AssertInterfaceType bool
	AssertMediaType     bool
}

func (InteractiveTargetDiskSettings) isTargetDiskSettings() {}
func (ScriptTargetDiskSettings) isTargetDiskSettings()      {}
func (FixedTargetDiskSettings) isTargetDiskSettings()       {}
func (GeneratedTargetDiskSettings) isTargetDiskSettings()   {}

type PartitionSettings interface{ isPartitionSettings() }

type InteractivePartitionSettings struct{}
type CustomPartitionSettings struct{ Script string }
type UnattendedPartitionSettings struct {
	TargetDiskSettings TargetDiskSettings
	PartitionLayout    PartitionLayout
	RecoveryMode       RecoveryMode
	SystemSize         int
	RecoverySize       int
}

func (InteractivePartitionSettings) isPartitionSettings() {}
func (CustomPartitionSettings) isPartitionSettings()      {}
func (UnattendedPartitionSettings) isPartitionSettings()  {}

type InstallFromSettings interface{ isInstallFromSettings() }

type InteractiveInstallFromSettings struct{}
type EditionInstallFromSettings struct{ Edition string } // WindowsEdition ID
type IndexInstallFromSettings struct{ Index int }
type NameInstallFromSettings struct{ Name string }

func (InteractiveInstallFromSettings) isInstallFromSettings() {}
func (EditionInstallFromSettings) isInstallFromSettings()     {}
func (IndexInstallFromSettings) isInstallFromSettings()       {}
func (NameInstallFromSettings) isInstallFromSettings()        {}

type PagingFileSettings interface{ isPagingFileSettings() }

type AutomaticPagingFileSettings struct{}
type CustomPagingFileSettings struct{ InitialSizeMiB, MaxSizeMiB int }
type NoPagingFileSettings struct{}

func (AutomaticPagingFileSettings) isPagingFileSettings() {}
func (CustomPagingFileSettings) isPagingFileSettings()    {}
func (NoPagingFileSettings) isPagingFileSettings()        {}

// Start menu and taskbar: IStartPinsSettings, IStartTilesSettings,
// ITaskbarIcons.
type StartPinsSettings interface{ isStartPinsSettings() }

type DefaultStartPinsSettings struct{}
type EmptyStartPinsSettings struct{}
type CustomStartPinsSettings struct{ Json string }

func (DefaultStartPinsSettings) isStartPinsSettings() {}
func (EmptyStartPinsSettings) isStartPinsSettings()   {}
func (CustomStartPinsSettings) isStartPinsSettings()  {}

type StartTilesSettings interface{ isStartTilesSettings() }

type DefaultStartTilesSettings struct{}
type EmptyStartTilesSettings struct{}
type CustomStartTilesSettings struct{ Xml string }

func (DefaultStartTilesSettings) isStartTilesSettings() {}
func (EmptyStartTilesSettings) isStartTilesSettings()   {}
func (CustomStartTilesSettings) isStartTilesSettings()  {}

type TaskbarIcons interface{ isTaskbarIcons() }

type DefaultTaskbarIcons struct{}
type EmptyTaskbarIcons struct{}
type CustomTaskbarIcons struct{ Xml string }

func (DefaultTaskbarIcons) isTaskbarIcons() {}
func (EmptyTaskbarIcons) isTaskbarIcons()   {}
func (CustomTaskbarIcons) isTaskbarIcons()  {}

// Visual effects: IEffects.
type Effects interface{ isEffects() }

type DefaultEffects struct{}
type BestPerformanceEffects struct{}
type BestAppearanceEffects struct{}
type EffectSetting struct {
	Key   Effect
	Value bool
}
type CustomEffects struct{ Settings []EffectSetting }

func (DefaultEffects) isEffects()         {}
func (BestPerformanceEffects) isEffects() {}
func (BestAppearanceEffects) isEffects()  {}
func (CustomEffects) isEffects()          {}

// Desktop icons and Start folders: IDesktopIconSettings, IStartFolderSettings.
type DesktopIconSettings interface{ isDesktopIconSettings() }

type DefaultDesktopIconSettings struct{}
type DesktopIconSetting struct {
	Key   string // DesktopIcon ID
	Value bool
}
type CustomDesktopIconSettings struct{ Settings []DesktopIconSetting }

func (DefaultDesktopIconSettings) isDesktopIconSettings() {}
func (CustomDesktopIconSettings) isDesktopIconSettings()  {}

type StartFolderSettings interface{ isStartFolderSettings() }

type DefaultStartFolderSettings struct{}
type StartFolderSetting struct {
	Key   string // StartFolder ID
	Value bool
}
type CustomStartFolderSettings struct{ Settings []StartFolderSetting }

func (DefaultStartFolderSettings) isStartFolderSettings() {}
func (CustomStartFolderSettings) isStartFolderSettings()  {}

// Sticky keys: IStickyKeysSettings.
type StickyKeysSettings interface{ isStickyKeysSettings() }

type DefaultStickyKeysSettings struct{}
type DisabledStickyKeysSettings struct{}
type CustomStickyKeysSettings struct{ Flags []StickyKeys }

func (DefaultStickyKeysSettings) isStickyKeysSettings()  {}
func (DisabledStickyKeysSettings) isStickyKeysSettings() {}
func (CustomStickyKeysSettings) isStickyKeysSettings()   {}
