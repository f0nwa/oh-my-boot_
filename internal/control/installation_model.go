package control

import "fmt"

const (
	BootProfileWindowsISO    = "windows_iso"
	BootProfileWinPEISO      = "winpe_iso"
	BootProfileUbuntuBuiltin = "ubuntu_builtin"
	BootProfileUbuntuISO     = "ubuntu_iso"
)

// BootProfile identifies a shared boot source, independent of menu entries.
// WindowsProfileID is zero for profiles without a Windows ISO.
type BootProfile struct {
	ID               int64
	Kind             string
	Title            string
	WindowsProfileID int64
	UbuntuSourceID   int64
	UbuntuStorageID  int64
	UbuntuPath       string
	UbuntuSHA256     string
	UbuntuState      string
}

// Installation is a persistent choice using one shared profile. Zero optional
// IDs mean manual installation; a driver configuration includes its own XML.
// AutoinstallID is Ubuntu-only; published menu items retain their own bindings.
type Installation struct {
	ID             int64
	BootProfileID  int64
	Title          string
	Folder         string
	AnswerID       int64
	DriverConfigID int64
	AutoinstallID  int64
}

// InstallationComponents freezes the optional components of a published entry.
// Its source is fixed by Installation.BootProfileID and never changes on edit.
type InstallationComponents struct {
	// Remember the entry's title/folder separately from menu-only overrides.
	Title          string `json:"title"`
	Folder         string `json:"folder"`
	AnswerID       int64  `json:"answer_id"`
	DriverConfigID int64  `json:"driver_config_id"`
	AutoinstallID  int64  `json:"autoinstall_id"`
}

func installationComponents(item Installation) *InstallationComponents {
	return &InstallationComponents{Title: item.Title, Folder: item.Folder, AnswerID: item.AnswerID, DriverConfigID: item.DriverConfigID, AutoinstallID: item.AutoinstallID}
}
func (c *InstallationComponents) apply(item Installation) Installation {
	if c != nil {
		item.AnswerID, item.DriverConfigID, item.AutoinstallID = c.AnswerID, c.DriverConfigID, c.AutoinstallID
	}
	return item
}
func sameInstallationComponents(a, b *InstallationComponents) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.AnswerID == b.AnswerID && a.DriverConfigID == b.DriverConfigID && a.AutoinstallID == b.AutoinstallID
}

// ValidateInstallation checks the type and ownership of the chosen components.
// Resource readiness is checked separately before creation and publication.
func ValidateInstallation(item Installation, profile BootProfile, answers []WindowsAnswerVariant, configs []DriverConfig, files []UbuntuAutoinstallSummary) error {
	if item.BootProfileID <= 0 || item.BootProfileID != profile.ID {
		return fmt.Errorf("invalid boot profile")
	}
	if !safeMenuText(item.Title, 80) || !isASCII(item.Title) || validateMenuFolder(item.Folder) != nil || !isASCII(item.Folder) {
		return fmt.Errorf("title and menu folder must be valid ASCII menu text")
	}
	if item.AutoinstallID < 0 {
		return fmt.Errorf("invalid Ubuntu Autoinstall file")
	}
	if item.AnswerID < 0 || item.DriverConfigID < 0 || (item.AnswerID > 0 && item.DriverConfigID > 0) {
		return fmt.Errorf("choose either an answer or a driver configuration")
	}
	switch profile.Kind {
	case BootProfileUbuntuBuiltin, BootProfileUbuntuISO:
		if profile.Kind == BootProfileUbuntuISO && (profile.UbuntuSourceID <= 0 || profile.UbuntuStorageID <= 0 || profile.UbuntuPath == "" || !ubuntuSourceDigest.MatchString(profile.UbuntuSHA256)) {
			return fmt.Errorf("Ubuntu source identity is invalid")
		}
		if item.AutoinstallID > 0 {
			validated := false
			for _, file := range files {
				if file.ID == item.AutoinstallID && file.State == "validated" {
					validated = true
					break
				}
			}
			if !validated {
				return fmt.Errorf("Ubuntu Autoinstall file is missing or not validated")
			}
		}
		if profile.WindowsProfileID != 0 || item.AnswerID != 0 || item.DriverConfigID != 0 {
			return fmt.Errorf("Ubuntu does not support Windows options")
		}
	case BootProfileWindowsISO, BootProfileWinPEISO:
		if profile.Kind == BootProfileWinPEISO && item.AnswerID != 0 {
			return fmt.Errorf("service WinPE does not support installer answers")
		}
		if item.AutoinstallID != 0 {
			return fmt.Errorf("Windows does not support Ubuntu Autoinstall files")
		}
		if profile.WindowsProfileID <= 0 {
			return fmt.Errorf("Windows boot profile has no ISO")
		}
		if item.AnswerID > 0 && !installationAnswerExists(item.AnswerID, answers) {
			return fmt.Errorf("answer scenario does not exist")
		}
		if item.DriverConfigID > 0 {
			for _, config := range configs {
				if config.ID == item.DriverConfigID && config.ProfileID == profile.WindowsProfileID {
					if profile.Kind == BootProfileWinPEISO && config.AnswerID != 0 {
						return fmt.Errorf("service WinPE driver configuration cannot contain installer answers")
					}
					if config.AnswerID > 0 && !installationAnswerExists(config.AnswerID, answers) {
						return fmt.Errorf("driver configuration answer scenario does not exist")
					}
					return nil
				}
			}
			return fmt.Errorf("driver configuration does not belong to the selected Windows profile")
		}
	default:
		return fmt.Errorf("unsupported boot profile kind %q", profile.Kind)
	}
	return nil
}

func installationAnswerExists(id int64, answers []WindowsAnswerVariant) bool {
	for _, answer := range answers {
		if answer.ID == id {
			return true
		}
	}
	return false
}

func isISOBootKind(kind string) bool {
	return kind == BootProfileWindowsISO || kind == BootProfileWinPEISO
}
