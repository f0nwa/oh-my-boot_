package control

import "fmt"

const (
	BootProfileWindowsISO    = "windows_iso"
	BootProfileUbuntuBuiltin = "ubuntu_builtin"
)

// BootProfile identifies a shared boot source, independent of menu entries.
// WindowsProfileID is zero for profiles without a Windows ISO.
type BootProfile struct {
	ID               int64
	Kind             string
	Title            string
	WindowsProfileID int64
}

// Installation is a persistent choice using one shared profile. Zero optional
// IDs mean manual installation; a driver configuration includes its own XML.
// AutoinstallID is Ubuntu-only and its binding never changes after creation.
type Installation struct {
	ID             int64
	BootProfileID  int64
	Title          string
	Folder         string
	AnswerID       int64
	DriverConfigID int64
	AutoinstallID  int64
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
	case BootProfileUbuntuBuiltin:
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
	case BootProfileWindowsISO:
		if item.AutoinstallID != 0 {
			return fmt.Errorf("Windows does not support Ubuntu Autoinstall files")
		}
		if profile.WindowsProfileID <= 0 {
			return fmt.Errorf("Windows boot profile has no ISO")
		}
		if item.AnswerID > 0 && !installationAnswerBelongs(item.AnswerID, profile.WindowsProfileID, answers) {
			return fmt.Errorf("answer does not belong to the selected Windows profile")
		}
		if item.DriverConfigID > 0 {
			for _, config := range configs {
				if config.ID == item.DriverConfigID && config.ProfileID == profile.WindowsProfileID {
					if config.AnswerID > 0 && !installationAnswerBelongs(config.AnswerID, profile.WindowsProfileID, answers) {
						return fmt.Errorf("driver configuration answer does not belong to the selected Windows profile")
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

func installationAnswerBelongs(id, profileID int64, answers []WindowsAnswerVariant) bool {
	for _, answer := range answers {
		if answer.ID == id {
			return answer.ProfileID == profileID
		}
	}
	return false
}
