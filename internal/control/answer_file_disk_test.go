package control

import (
	"encoding/json"
	"omb/internal/unattend"
	"os"
	"strings"
	"testing"
)

func TestAnswerFileDiskXML(t *testing.T) {
	wrap := func(s string) []byte {
		return []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings pass="windowsPE"><component name="Microsoft-Windows-Setup">` + s + `</component></settings></unattend>`)
	}
	for _, tc := range []struct {
		name         string
		xml          []byte
		kind, target string
		danger       bool
	}{
		{"no disk", wrap(`<UserData/>`), "unspecified", "Авторазметка не обнаружена в XML", false},
		{"wipe", wrap(`<DiskConfiguration><Disk><DiskID>2</DiskID><WillWipeDisk>true</WillWipeDisk></Disk></DiskConfiguration>`), "wipe", "Диск 2", true},
		{"partitions", wrap(`<DiskConfiguration><Disk><DiskID>1</DiskID><CreatePartitions><CreatePartition/></CreatePartitions></Disk></DiskConfiguration>`), "partition", "Диск 1", true},
		{"install target", wrap(`<ImageInstall><OSImage><InstallTo><DiskID>3</DiskID><PartitionID>4</PartitionID></InstallTo></OSImage></ImageInstall>`), "install", "Диск 3 · раздел 4", true},
		{"commands", wrap(`<RunSynchronous><RunSynchronousCommand><Path>powershell -File custom.ps1</Path></RunSynchronousCommand></RunSynchronous>`), "script", "Целевой диск не определён", true},
		{"malformed", []byte(`<unattend>`), "unknown", "Нет данных о разметке", false},
		{"multiple disks", wrap(`<DiskConfiguration><Disk><DiskID>0</DiskID><WillWipeDisk>true</WillWipeDisk></Disk><Disk><DiskID>1</DiskID><WillWipeDisk>true</WillWipeDisk></Disk></DiskConfiguration>`), "wipe", "Диски: 0, 1", true},
		{"foreign namespace", []byte(`<unattend xmlns="untrusted"><DiskConfiguration><WillWipeDisk>true</WillWipeDisk></DiskConfiguration></unattend>`), "unknown", "Нет данных о разметке", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SummarizeWindowsAnswerDisk(tc.xml)
			if got.Kind != tc.kind || got.Target != tc.target || got.Danger() != tc.danger {
				t.Fatalf("disk=%+v danger=%v; want %s %s %v", got, got.Danger(), tc.kind, tc.target, tc.danger)
			}
		})
	}
}

func TestAnswerFileDiskGenerated(t *testing.T) {
	raw, err := os.ReadFile("../unattend/testdata/golden/pe-gpt-disk0.json")
	if err != nil {
		t.Fatal(err)
	}
	var c unattend.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	d, err := unattend.LoadData()
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := unattend.Build(d, c)
	if err != nil {
		t.Fatal(err)
	}
	got := SummarizeWindowsAnswerDisk(data)
	if got.Kind != "wipe" || got.Target != "Диск 0" {
		t.Fatalf("generated disk=%+v", got)
	}
	modified := strings.Replace(string(data), "</unattend>", "<!-- changed --> </unattend>", 1)
	if modified == string(data) {
		t.Fatal("fixture did not change")
	}
	if got := SummarizeWindowsAnswerDisk([]byte(modified)); got.Kind != "script" || got.Target == "Диск 0" {
		t.Fatalf("trusted stale metadata: %+v", got)
	}
}
