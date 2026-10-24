package unattend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

func loadTestData(t *testing.T) *Data {
	t.Helper()
	d, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestOpenEditorFile: a file the editor made opens to the same settings.
func TestOpenEditorFile(t *testing.T) {
	d := loadTestData(t)
	files, _ := filepath.Glob("testdata/golden/*.json")
	for _, file := range files {
		raw, _ := os.ReadFile(file)
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		out, _, err := Build(d, c)
		if err != nil {
			continue // the error goldens
		}
		back, report, err := Open(d, out)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !report.Exact || report.Modified {
			t.Errorf("%s: report %+v", file, report)
		}
		if !reflect.DeepEqual(back, c) {
			t.Errorf("%s: settings changed on the way", file)
		}
	}
}

func TestOpenEditorFileModified(t *testing.T) {
	d := loadTestData(t)
	out, _, err := Build(d, Default())
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(out), "<ProtectYourPC>3</ProtectYourPC>", "<ProtectYourPC>1</ProtectYourPC>", 1)
	_, report, err := Open(d, []byte(edited))
	if err != nil || !report.Exact || !report.Modified {
		t.Fatalf("report %+v, err %v", report, err)
	}
}

// openRebuild opens a foreign file and builds it again.
func openRebuild(t *testing.T, d *Data, data []byte) (Config, ImportReport, *Document) {
	t.Helper()
	c, report, err := Open(d, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Lost) > 0 {
		t.Fatalf("lost: %v", report.Lost)
	}
	out, _, err := Build(d, c)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseXML(out)
	if err != nil {
		t.Fatal(err)
	}
	return c, report, doc
}

// Values the import moves on purpose: the script sequences of Schneegans
// files run again from new paths, by new commands; ComputerName "*" is the
// generator's random name, which writes no element.
var movedLeaf = regexp.MustCompile(`(?i)^root\|Extensions/(Build/|ExtractScript=|File\[c:\\windows\\setup\\scripts\\(specialize|firstlogon|useronce|defaultuser)\.ps1\])|(Specialize|DefaultUser|FirstLogon)\.ps1|Extensions\.ExtractScript|reg\.exe (load|unload) "HKU\\DefaultUser"|\|ComputerName=\*$`)

func checkNothingLost(t *testing.T, name string, src []byte, out *Document) {
	t.Helper()
	srcDoc, err := ParseXML(mustUTF8(t, src))
	if err != nil {
		t.Fatal(err)
	}
	have := leavesOf(out.Root())
	for k := range leavesOf(srcDoc.Root()) {
		if !have[k] && !movedLeaf.MatchString(k) {
			t.Errorf("%s: missing %s", name, k)
		}
	}
}

func mustUTF8(t *testing.T, b []byte) []byte {
	b, err := toUTF8(b)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestOpenSchneegansFile: output of the original opens with nothing lost.
func TestOpenSchneegansFile(t *testing.T) {
	d := loadTestData(t)
	files, _ := filepath.Glob("testdata/golden/*.xml")
	for _, file := range files {
		src, _ := os.ReadFile(file)
		_, _, out := openRebuild(t, d, src)
		checkNothingLost(t, filepath.Base(file), src, out)
	}
}

// TestOpenForeignFiles: answer files from the server and by hand.
func TestOpenForeignFiles(t *testing.T) {
	d := loadTestData(t)
	files, _ := filepath.Glob("testdata/foreign/*.xml")
	for _, file := range files {
		src, _ := os.ReadFile(file)
		_, report, out := openRebuild(t, d, src)
		checkNothingLost(t, filepath.Base(file), src, out)
		t.Logf("%s: recognized %v; kept %v", filepath.Base(file), report.Recognized, report.KeptAsXML)
	}
}

func TestOpenForeignKeepsUnknown(t *testing.T) {
	d := loadTestData(t)
	src, _ := os.ReadFile("testdata/foreign/hand-made.xml")
	c, report, out := openRebuild(t, d, src)
	s := string(Serialize(out))
	for _, want := range []string{
		"<JoinDomain>corp.example</JoinDomain>", // unknown component
		"<RegisteredOrganization>",              // unknown element in a known component
		"<SkipMachineOOBE>true</SkipMachineOOBE>",
		"<FullName>",
		`cpi:source="wim:c:/install.wim#Windows 11 Pro"`,
		"<WillWipeDisk>true</WillWipeDisk>",
		"cmd.exe /c echo custom",
		"cmd /c echo hello",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("result lacks %s", want)
		}
	}
	for _, want := range []string{"language", "timezone", "computer-name", "bypass-checks", "accounts", "express-settings"} {
		found := false
		for _, r := range report.Recognized {
			found = found || r == want
		}
		if !found {
			t.Errorf("%s not recognized: %v", want, report.Recognized)
		}
	}
	if _, ok := c.TimeZoneSettings.(ExplicitTimeZoneSettings); !ok {
		t.Errorf("time zone: %#v", c.TimeZoneSettings)
	}
	if report.DroppedComments != 1 {
		t.Errorf("comments: %d", report.DroppedComments)
	}
	// The bypass commands are not doubled.
	if n := strings.Count(s, "BypassTPMCheck"); n != 1 {
		t.Errorf("BypassTPMCheck %d times", n)
	}
	// Saved by the editor, the file opens exactly.
	again, _, err := Build(d, c)
	if err != nil {
		t.Fatal(err)
	}
	back, r2, err := Open(d, again)
	if err != nil || !r2.Exact || r2.Modified || !reflect.DeepEqual(back, c) {
		t.Fatalf("reopen: %+v %v", r2, err)
	}
}

func TestOpenUTF16(t *testing.T) {
	d := loadTestData(t)
	src, _ := os.ReadFile("testdata/foreign/key-no-disk.xml")
	s := strings.Replace(string(src), `encoding="utf-8"`, `encoding="utf-16"`, 1)
	units := utf16.Encode([]rune(s))
	raw := []byte{0xFF, 0xFE}
	for _, u := range units {
		raw = append(raw, byte(u), byte(u>>8))
	}
	_, report, err := Open(d, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Lost) > 0 {
		t.Fatal(report.Lost)
	}
}

func TestOpenRejectsNonAnswer(t *testing.T) {
	d := loadTestData(t)
	for _, s := range []string{"", "<a/>", "<unattend/>", "not xml"} {
		if _, _, err := Open(d, []byte(s)); err == nil {
			t.Errorf("%q: want an error", s)
		}
	}
}
