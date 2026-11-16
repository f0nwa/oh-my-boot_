package control

import (
	"reflect"
	"strings"
	"testing"

	"omb/internal/agent"
)

func TestAnswerCompatibilitySummarizeGeneratedAndForeignXML(t *testing.T) {
	t.Run("unattend generated amd64 pro ru-RU bypass GPT", func(t *testing.T) {
		xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
	<settings pass="windowsPE">
		<component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
			<SetupUILanguage>
				<UILanguage>ru-RU</UILanguage>
			</SetupUILanguage>
			<SystemLocale>ru-RU</SystemLocale>
			<UILanguage>ru-RU</UILanguage>
			<UserLocale>ru-RU</UserLocale>
			<InputLocale>0419:00000419</InputLocale>
		</component>
		<component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
			<DiskConfiguration>
				<Disk wcm:action="add">
					<DiskID>0</DiskID>
					<WillWipeDisk>true</WillWipeDisk>
					<CreatePartitions>
						<CreatePartition wcm:action="add">
							<Order>1</Order>
							<Type>EFI</Type>
							<Size>300</Size>
						</CreatePartition>
						<CreatePartition wcm:action="add">
							<Order>2</Order>
							<Type>MSR</Type>
							<Size>16</Size>
						</CreatePartition>
						<CreatePartition wcm:action="add">
							<Order>3</Order>
							<Type>Primary</Type>
							<Extend>true</Extend>
						</CreatePartition>
					</CreatePartitions>
				</Disk>
			</DiskConfiguration>
			<ImageInstall>
				<OSImage>
					<InstallFrom>
						<MetaData wcm:action="add">
							<Key>/IMAGE/NAME</Key>
							<Value>Windows 11 Pro</Value>
						</MetaData>
					</InstallFrom>
					<InstallTo>
						<DiskID>0</DiskID>
						<PartitionID>3</PartitionID>
					</InstallTo>
				</OSImage>
			</ImageInstall>
			<UserData>
				<AcceptEula>true</AcceptEula>
				<ProductKey>
					<Key>VK7JG-NPHTM-C97JM-9MPGT-3V66T</Key>
					<WillShowUI>OnError</WillShowUI>
				</ProductKey>
			</UserData>
			<RunSynchronous>
				<RunSynchronousCommand wcm:action="add">
					<Order>1</Order>
					<Path>reg.exe add "HKLM\SYSTEM\Setup\LabConfig" /v BypassTPMCheck /t REG_DWORD /d 1 /f</Path>
				</RunSynchronousCommand>
			</RunSynchronous>
		</component>
	</settings>
	<settings pass="specialize">
		<component name="Microsoft-Windows-Shell-Setup" processorArchitecture="x86" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
			<ComputerName>PC-TEST</ComputerName>
		</component>
	</settings>
</unattend>`)

		target := SummarizeWindowsAnswerTarget(xmlData)
		if !reflect.DeepEqual(target.Architectures, []string{"amd64"}) {
			t.Fatalf("Architectures = %#v, want [amd64] (helper x86 ignored when amd64 present)", target.Architectures)
		}
		if target.ImageMode != "name" || target.ImageName != "Windows 11 Pro" {
			t.Fatalf("image selection = (%q, %q), want (name, Windows 11 Pro)", target.ImageMode, target.ImageName)
		}
		if target.ImageLanguage != "ru-RU" {
			t.Fatalf("ImageLanguage = %q, want ru-RU", target.ImageLanguage)
		}
		if !target.Win11Only {
			t.Fatalf("Win11Only = false, want true")
		}
		if target.PartitionLayout != "GPT" {
			t.Fatalf("PartitionLayout = %q, want GPT", target.PartitionLayout)
		}
		if target.ProductKeyMode != "custom" {
			t.Fatalf("ProductKeyMode = %q, want custom", target.ProductKeyMode)
		}
	})

	t.Run("foreign XML by IMAGE INDEX MBR LabConfig", func(t *testing.T) {
		xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
	<settings pass="windowsPE">
		<component name="Microsoft-Windows-International-Core-WinPE" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
			<SetupUILanguage>
				<UILanguage>en-US</UILanguage>
			</SetupUILanguage>
		</component>
		<component name="Microsoft-Windows-Setup" processorArchitecture="amd64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
			<DiskConfiguration>
				<Disk wcm:action="add">
					<DiskID>0</DiskID>
					<WillWipeDisk>true</WillWipeDisk>
					<CreatePartitions>
						<CreatePartition wcm:action="add">
							<Order>1</Order>
							<Type>Primary</Type>
							<Size>100</Size>
						</CreatePartition>
						<CreatePartition wcm:action="add">
							<Order>2</Order>
							<Type>Primary</Type>
							<Extend>true</Extend>
						</CreatePartition>
					</CreatePartitions>
					<ModifyPartitions>
						<ModifyPartition wcm:action="add">
							<Order>1</Order>
							<PartitionID>1</PartitionID>
							<Active>true</Active>
							<Format>NTFS</Format>
						</ModifyPartition>
					</ModifyPartitions>
				</Disk>
			</DiskConfiguration>
			<ImageInstall>
				<OSImage>
					<InstallFrom>
						<MetaData wcm:action="add">
							<Key>/IMAGE/INDEX</Key>
							<Value>2</Value>
						</MetaData>
					</InstallFrom>
				</OSImage>
			</ImageInstall>
			<UserData>
				<ProductKey>
					<WillShowUI>OnError</WillShowUI>
				</ProductKey>
			</UserData>
		</component>
	</settings>
</unattend>`)

		target := SummarizeWindowsAnswerTarget(xmlData)
		if target.ImageMode != "index" || target.ImageIndex != 2 {
			t.Fatalf("image selection = (%q, %d), want (index, 2)", target.ImageMode, target.ImageIndex)
		}
		if target.PartitionLayout != "MBR" {
			t.Fatalf("PartitionLayout = %q, want MBR", target.PartitionLayout)
		}
		if target.ImageLanguage != "en-US" {
			t.Fatalf("ImageLanguage = %q, want en-US", target.ImageLanguage)
		}
		if target.ProductKeyMode != "firmware" {
			t.Fatalf("ProductKeyMode = %q, want firmware", target.ProductKeyMode)
		}
	})

	t.Run("foreign XML by IMAGE EDITIONID and arm64", func(t *testing.T) {
		xmlData := []byte(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend" xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
	<settings pass="windowsPE">
		<component name="Microsoft-Windows-Setup" processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
			<ImageInstall>
				<OSImage>
					<InstallFrom>
						<MetaData wcm:action="add">
							<Key>/IMAGE/EDITIONID</Key>
							<Value>Professional</Value>
						</MetaData>
					</InstallFrom>
				</OSImage>
			</ImageInstall>
		</component>
	</settings>
</unattend>`)

		target := SummarizeWindowsAnswerTarget(xmlData)
		if !reflect.DeepEqual(target.Architectures, []string{"arm64"}) {
			t.Fatalf("Architectures = %#v, want [arm64]", target.Architectures)
		}
		if target.ImageMode != "edition" || target.EditionID != "Professional" {
			t.Fatalf("image selection = (%q, %q), want (edition, Professional)", target.ImageMode, target.EditionID)
		}
	})
}

func TestAnswerCompatibilityChips(t *testing.T) {
	t.Run("full pro amd64 win11 firmware ru-RU", func(t *testing.T) {
		target := WindowsAnswerTarget{
			Architectures:  []string{"amd64"},
			Win11Only:      true,
			ImageMode:      "edition",
			EditionID:      "pro",
			EditionLabel:   "Pro",
			ProductKeyMode: "firmware",
			ImageLanguage:  "ru-RU",
		}
		got := target.Chips()
		want := []string{"amd64", "Win 11", "Pro", "ключ BIOS", "ru-RU"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Chips() = %#v, want %#v", got, want)
		}
	})

	t.Run("index with custom key", func(t *testing.T) {
		target := WindowsAnswerTarget{
			Architectures:  []string{"amd64"},
			ImageMode:      "index",
			ImageIndex:     2,
			ProductKeyMode: "custom",
		}
		got := target.Chips()
		want := []string{"amd64", "индекс 2", "свой ключ"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Chips() = %#v, want %#v", got, want)
		}
	})

	t.Run("any architecture any edition", func(t *testing.T) {
		target := WindowsAnswerTarget{}
		got := target.Chips()
		want := []string{"любая арх.", "любая редакция"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Chips() = %#v, want %#v", got, want)
		}
	})
}

func TestEvaluateWindowsAnswerCompatibility(t *testing.T) {
	win11WIM := agent.WindowsWIMInfo{
		Known: true,
		Images: []agent.WindowsWIMImage{
			{
				Index:           1,
				Name:            "Windows 11 Home",
				DisplayName:     "Windows 11 Домашняя",
				Architecture:    "amd64",
				EditionID:       "Core",
				Languages:       []string{"ru-RU"},
				DefaultLanguage: "ru-RU",
				Build:           26100,
			},
			{
				Index:           2,
				Name:            "Windows 11 Pro",
				DisplayName:     "Windows 11 Pro",
				Architecture:    "amd64",
				EditionID:       "Professional",
				Languages:       []string{"ru-RU", "en-US"},
				DefaultLanguage: "ru-RU",
				Build:           26100,
			},
		},
	}

	win10WIM := agent.WindowsWIMInfo{
		Known: true,
		Images: []agent.WindowsWIMImage{
			{
				Index:           1,
				Name:            "Windows 10 Pro",
				DisplayName:     "Windows 10 Pro",
				Architecture:    "amd64",
				EditionID:       "Professional",
				Languages:       []string{"en-US"},
				DefaultLanguage: "en-US",
				Build:           19045,
			},
		},
	}

	t.Run("compatible edition match", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "edition",
			EditionID:     "pro",
			EditionLabel:  "Pro",
			ImageLanguage: "ru-RU",
			Win11Only:     true,
		}, win11WIM)
		if res.Status != "compatible" {
			t.Fatalf("Status = %q (errors=%v warnings=%v), want compatible", res.Status, res.Errors, res.Warnings)
		}
		if !strings.Contains(res.MatchedSummary, "2") || !strings.Contains(res.MatchedSummary, "Pro") {
			t.Fatalf("MatchedSummary = %q, expected index 2 & Pro", res.MatchedSummary)
		}
	})

	t.Run("layer 1 architecture mismatch", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures: []string{"arm64"},
		}, win11WIM)
		if res.Status != "incompatible" || len(res.Errors) == 0 {
			t.Fatalf("got %+v, want incompatible with errors", res)
		}
	})

	t.Run("layer 1 missing index", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "index",
			ImageIndex:    6,
		}, win11WIM)
		if res.Status != "incompatible" || len(res.Errors) == 0 {
			t.Fatalf("got %+v, want incompatible for missing index", res)
		}
	})

	t.Run("layer 1 missing name", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "name",
			ImageName:     "Windows 11 Enterprise",
		}, win11WIM)
		if res.Status != "incompatible" || len(res.Errors) == 0 {
			t.Fatalf("got %+v, want incompatible for missing image name", res)
		}
	})

	t.Run("layer 1 missing edition", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "edition",
			EditionID:     "enterprise",
			EditionLabel:  "Enterprise",
		}, win11WIM)
		if res.Status != "incompatible" || len(res.Errors) == 0 {
			t.Fatalf("got %+v, want incompatible for missing edition", res)
		}
	})

	t.Run("layer 2 warnings Win11Only language MBR", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures:   []string{"amd64"},
			ImageMode:       "edition",
			EditionID:       "pro",
			ImageLanguage:   "ru-RU",
			PartitionLayout: "MBR",
			Win11Only:       true,
		}, win10WIM)
		if res.Status != "warning" {
			t.Fatalf("Status = %q, want warning (res=%+v)", res.Status, res)
		}
		if len(res.Warnings) != 3 {
			t.Fatalf("Warnings count = %d (%v), want 3 (Win11Only + ImageLanguage + MBR)", len(res.Warnings), res.Warnings)
		}
	})

	t.Run("unknown WIM allows compatibility unless MBR", func(t *testing.T) {
		res := EvaluateWindowsAnswerCompatibility(WindowsAnswerTarget{
			Architectures: []string{"amd64"},
			ImageMode:     "edition",
			EditionID:     "pro",
			Win11Only:     true,
		}, agent.WindowsWIMInfo{Known: false})
		if res.Status != "compatible" {
			t.Fatalf("Status = %q, want compatible when WIM unknown", res.Status)
		}
	})
}
