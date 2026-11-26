package control

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"omb/internal/unattend"
)

// AnswerFileDisk is a descriptive hint, never a guarantee that scripts preserve data.
// It stores no script bodies, credentials or other answer-file contents.
type AnswerFileDisk struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

func (d AnswerFileDisk) Danger() bool {
	return d.Kind == "wipe" || d.Kind == "partition" || d.Kind == "install" || d.Kind == "script"
}
func (d AnswerFileDisk) Title() string {
	switch d.Kind {
	case "wipe":
		return "Стирает диск"
	case "partition":
		return "Изменяет разделы"
	case "install":
		return "Установка в раздел"
	case "script":
		return "Действия задаёт скрипт"
	case "interactive":
		return "Выбор вручную"
	default:
		return "Не определён"
	}
}
func (d AnswerFileDisk) Detail() string {
	if d.Target != "" {
		return d.Target
	}
	return "Нет данных о разметке"
}

type answerDiskXML struct {
	XMLName  xml.Name
	Name     string          `xml:"name,attr"`
	Pass     string          `xml:"pass,attr"`
	Text     string          `xml:",chardata"`
	Children []answerDiskXML `xml:",any"`
}

func (n answerDiskXML) child(name string) answerDiskXML {
	for _, c := range n.Children {
		if c.XMLName.Local == name && c.XMLName.Space == windowsAnswerNamespace {
			return c
		}
	}
	return answerDiskXML{}
}
func (n answerDiskXML) number(name string) string {
	v := strings.TrimSpace(n.child(name).Text)
	x, err := strconv.Atoi(v)
	if err != nil || x < 0 {
		return ""
	}
	return strconv.Itoa(x)
}

// SummarizeWindowsAnswerDisk reads a whole XML before reporting known settings.
// Only unchanged editor output may use its embedded settings; foreign commands
// take precedence over declarative settings because their effects are unknown.
func SummarizeWindowsAnswerDisk(data []byte) AnswerFileDisk {
	unknown := AnswerFileDisk{Kind: "unknown", Target: "Нет данных о разметке"}
	if ValidateWindowsAnswer(data) != nil {
		return unknown
	}
	if d, err := unattend.LoadData(); err == nil {
		if c, report, err := unattend.Open(d, data); err == nil && report.Exact && !report.Modified && len(c.ExtraXML) == 0 && len(c.Components) == 0 && len(c.ScriptSettings.Scripts) == 0 && len(c.ImportedScripts) == 0 {
			switch pe := c.PESettings.(type) {
			case unattend.GeneratePESettings:
				if partition, ok := pe.PartitionSettings.(unattend.UnattendedPartitionSettings); ok {
					target := "Диск подбирается по условиям"
					switch disk := partition.TargetDiskSettings.(type) {
					case unattend.FixedTargetDiskSettings:
						target = fmt.Sprintf("Диск %d", disk.Index)
					case unattend.InteractiveTargetDiskSettings:
						target = "Диск выбирается перед разметкой"
					case unattend.ScriptTargetDiskSettings:
						return AnswerFileDisk{Kind: "script", Target: "Целевой диск задаёт скрипт"}
					}
					return AnswerFileDisk{Kind: "wipe", Target: target}
				}
				if _, ok := pe.PartitionSettings.(unattend.CustomPartitionSettings); ok {
					return AnswerFileDisk{Kind: "script", Target: "Целевой диск задаёт скрипт"}
				}
				return AnswerFileDisk{Kind: "interactive", Target: "Разметка в установщике"}
			case unattend.ScriptPESetttings:
				return AnswerFileDisk{Kind: "script", Target: "Целевой диск не определён"}
			}
		}
	}
	var root answerDiskXML
	if xml.Unmarshal(data, &root) != nil {
		return unknown
	}
	result := AnswerFileDisk{Kind: "unspecified", Target: "Авторазметка не обнаружена в XML"}
	var diskIDs []string
	seen := map[string]bool{}
	commands := false
	var walk func(answerDiskXML, bool)
	walk = func(n answerDiskXML, setup bool) {
		if n.XMLName.Space != windowsAnswerNamespace {
			return
		}
		if n.XMLName.Local == "settings" {
			setup = n.Pass == "windowsPE"
		}
		if n.XMLName.Local == "component" {
			setup = setup && n.Name == "Microsoft-Windows-Setup"
		}
		if n.XMLName.Local == "RunSynchronousCommand" || n.XMLName.Local == "RunAsynchronousCommand" || n.XMLName.Local == "SynchronousCommand" || n.XMLName.Local == "AsynchronousCommand" {
			commands = true
		}
		if setup && n.XMLName.Local == "DiskConfiguration" {
			for _, disk := range n.Children {
				if disk.XMLName.Local != "Disk" || disk.XMLName.Space != windowsAnswerNamespace {
					continue
				}
				wipe := strings.EqualFold(strings.TrimSpace(disk.child("WillWipeDisk").Text), "true")
				partition := len(disk.child("CreatePartitions").Children) > 0 || len(disk.child("ModifyPartitions").Children) > 0
				if wipe {
					result.Kind = "wipe"
				} else if partition && result.Kind != "wipe" {
					result.Kind = "partition"
				}
				if wipe || partition {
					if id := disk.number("DiskID"); id != "" && !seen[id] {
						diskIDs = append(diskIDs, id)
						seen[id] = true
					}
				}
			}
		}
		if setup && n.XMLName.Local == "InstallTo" && result.Kind == "unspecified" {
			result = AnswerFileDisk{Kind: "install", Target: "Целевой диск не определён"}
			if id := n.number("DiskID"); id != "" {
				result.Target = "Диск " + id
				if part := n.number("PartitionID"); part != "" {
					result.Target = fmt.Sprintf("Диск %s · раздел %s", id, part)
				}
			}
		}
		for _, c := range n.Children {
			walk(c, setup)
		}
	}
	walk(root, false)
	if commands {
		return AnswerFileDisk{Kind: "script", Target: "Целевой диск не определён"}
	}
	if result.Kind == "wipe" || result.Kind == "partition" {
		result.Target = "Целевой диск не определён"
		if len(diskIDs) == 1 {
			result.Target = "Диск " + diskIDs[0]
		} else if len(diskIDs) > 1 {
			result.Target = "Диски: " + strings.Join(diskIDs, ", ")
		}
	}
	return result
}

func ubuntuAutoinstallDiskSummary(diskMode, diskSerial string) AnswerFileDisk {
	switch diskMode {
	case "serial":
		return AnswerFileDisk{Kind: "wipe", Target: "Серийный номер: " + diskSerial}
	case "default":
		return AnswerFileDisk{Kind: "wipe", Target: "Диск выбирает установщик"}
	case "interactive":
		return AnswerFileDisk{Kind: "interactive", Target: "Разметка в установщике"}
	case "custom":
		return AnswerFileDisk{Kind: "partition", Target: "Своя разметка (YAML)"}
	default:
		return AnswerFileDisk{Kind: "unknown", Target: "Нет данных о разметке"}
	}
}

func (f UbuntuAutoinstallSummary) DiskSummary() AnswerFileDisk {
	return ubuntuAutoinstallDiskSummary(f.DiskMode, f.DiskSerial)
}

func (f UbuntuAutoinstallFile) DiskSummary() AnswerFileDisk {
	mode, serial := f.DiskMode, f.DiskSerial
	if mode == "" {
		mode, serial = f.Values.DiskMode, f.Values.DiskSerial
	}
	return ubuntuAutoinstallDiskSummary(mode, serial)
}

func (v WindowsAnswerVariant) DiskSummary() AnswerFileDisk {
	if v.Disk.Kind != "" {
		return v.Disk
	}
	if v.WipesDisk != nil && *v.WipesDisk {
		return AnswerFileDisk{Kind: "partition", Target: "Целевой диск не определён"}
	}
	return AnswerFileDisk{Kind: "unknown", Target: "Нет данных о разметке"}
}
