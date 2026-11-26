package control

import (
	"testing"

	"omb/internal/agent"
)

func TestCheckBootInstallCompat(t *testing.T) {
	win11Install := agent.WindowsWIMInfo{
		Known: true,
		Images: []agent.WindowsWIMImage{
			{Index: 1, Name: "Windows 11 Pro", Architecture: "amd64", EditionID: "Professional", Build: 26100},
		},
	}
	arm64Install := agent.WindowsWIMInfo{
		Known: true,
		Images: []agent.WindowsWIMImage{
			{Index: 1, Name: "Windows 11 Pro ARM64", Architecture: "arm64", EditionID: "Professional", Build: 26100},
		},
	}

	t.Run("win11_boot_with_setup_and_win11_install_is_compatible", func(t *testing.T) {
		bootImg := WindowsBootImage{
			ID:          1,
			HasSetupExe: true,
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Microsoft Windows PE (amd64)", Architecture: "amd64", Build: 26100},
					{Index: 2, Name: "Microsoft Windows Setup (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		}
		got := CheckBootInstallCompat(bootImg, win11Install, false)
		if got.Status != "compatible" || len(got.Errors) != 0 || len(got.Warnings) != 0 {
			t.Fatalf("got %+v, want compatible without warnings/errors", got)
		}
	})

	t.Run("win10_boot_with_win11_install_is_warning", func(t *testing.T) {
		bootImg := WindowsBootImage{
			ID:          2,
			HasSetupExe: true,
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Microsoft Windows PE (amd64)", Architecture: "amd64", Build: 19041},
					{Index: 2, Name: "Microsoft Windows Setup (amd64)", Architecture: "amd64", Build: 19041},
				},
			},
		}
		got := CheckBootInstallCompat(bootImg, win11Install, false)
		if got.Status != "warning" || len(got.Warnings) == 0 || len(got.Errors) != 0 {
			t.Fatalf("got %+v, want warning without errors", got)
		}
	})

	t.Run("adk_boot_without_setup_and_bare_install_is_incompatible", func(t *testing.T) {
		bootImg := WindowsBootImage{
			ID:          3,
			HasSetupExe: false,
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Microsoft Windows PE (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		}
		got := CheckBootInstallCompat(bootImg, win11Install, false)
		if got.Status != "incompatible" || len(got.Errors) == 0 {
			t.Fatalf("got %+v, want incompatible with errors", got)
		}
	})

	t.Run("adk_boot_without_setup_and_windows_iso_is_compatible", func(t *testing.T) {
		bootImg := WindowsBootImage{
			ID:          4,
			HasSetupExe: false,
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 1, Name: "Microsoft Windows PE (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		}
		got := CheckBootInstallCompat(bootImg, win11Install, true)
		if got.Status != "compatible" || len(got.Errors) != 0 {
			t.Fatalf("got %+v, want compatible", got)
		}
	})

	t.Run("x64_boot_and_arm64_install_is_incompatible", func(t *testing.T) {
		bootImg := WindowsBootImage{
			ID:          5,
			HasSetupExe: true,
			WIMInfo: agent.WindowsWIMInfo{
				Known: true,
				Images: []agent.WindowsWIMImage{
					{Index: 2, Name: "Microsoft Windows Setup (amd64)", Architecture: "amd64", Build: 26100},
				},
			},
		}
		got := CheckBootInstallCompat(bootImg, arm64Install, true)
		if got.Status != "incompatible" || len(got.Errors) == 0 {
			t.Fatalf("got %+v, want incompatible", got)
		}
	})
}
