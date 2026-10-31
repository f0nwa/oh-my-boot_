package ombmigration

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestPathConversionHonorsComponentBoundaries(t *testing.T) {
	for _, c := range []struct{ in, want string }{{"/srv/pxe/tftp", "/srv/omb/tftp"}, {"/srv/pxe-user/file.iso", "/srv/pxe-user/file.iso"}, {"//nas/pxeisos", "//nas/pxeisos"}, {"/mnt/pxe-platform-smb-upload/key/pxe.iso", "/mnt/omb-smb-upload/key/pxe.iso"}} {
		if got := MapPath(c.in); got != c.want {
			t.Errorf("%q: %q", c.in, got)
		}
	}
}
func TestXMLConversionPreservesForeignBytesAndRejectsAmbiguousScript(t *testing.T) {
	in := []byte(`<?xml version="1.0"?><u><Editor xmlns='urn:pxe-platform:unattend-editor'>{"owner":"PXE User"}</Editor><foreign xmlns="urn:other" a='1'><!--keep--> pxE </foreign></u>`)
	got, err := ConvertXML(in)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(in, []byte("urn:pxe-platform:unattend-editor"), []byte("urn:omb:unattend-editor"), 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("foreign bytes changed: %s", got)
	}
	if _, err := ConvertXML([]byte(`<u><CommandLine>cmd /c pxe-setup.cmd</CommandLine></u>`)); err == nil {
		t.Fatal("accepted ambiguous user script")
	}
	if _, err := ConvertXML([]byte(`<u>`)); err == nil {
		t.Fatal("accepted invalid XML")
	}
}
func TestMenuConversionUpdatesReferencesWithoutChangingTitles(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(`[{"id":3,"profile_id":2,"sha256":"old"}]`))
	in := "#!ipxe\n# pxe-platform:folders-v1 e30\n# pxe-platform:winanswers-v1 " + encoded + "\n# pxe-platform:look pult-4\nitem test pxe-setup.cmd remains a title\ninitrd http://host/assets/windows-answers/3/pxe-answer.xml pxe-answer.xml\niseq ${pxe_fb} 1 && set pxe_fw BIOS\n"
	got, err := ConvertMenu([]byte(in), Replacements{Answers: map[int64]AnswerChange{3: {ProfileID: 2, OldHash: "old", NewHash: "new"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "item test pxe-setup.cmd remains a title") || !strings.Contains(s, "/omb-answer.xml omb-answer.xml") || !strings.Contains(s, "${omb_fb}") {
		t.Fatal(s)
	}
	lines := strings.Split(s, "\n")
	payload, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(lines[2], "# omb:winanswers-v1 "))
	if !strings.Contains(string(payload), `"sha256":"new"`) {
		t.Fatal(string(payload))
	}
	if _, err := ConvertMenu([]byte(strings.Replace(in, "e30", "bad!", 1)), Replacements{}); err == nil {
		t.Fatal("accepted malformed metadata")
	}
	if _, err := ConvertMenu([]byte(in), Replacements{}); err == nil {
		t.Fatal("accepted unresolved answer")
	}
}
func TestXMLDoesNotRewriteNamespaceTextInsideForeignAttribute(t *testing.T) {
	in := []byte(`<u note="xmlns='urn:pxe-platform:unattend-editor'"><Editor xmlns="urn:pxe-platform:unattend-editor"/></u>`)
	got, err := ConvertXML(in)
	if err != nil {
		t.Fatal(err)
	}
	want := `<u note="xmlns='urn:pxe-platform:unattend-editor'"><Editor xmlns="urn:omb:unattend-editor"/></u>`
	if string(got) != want {
		t.Fatal(string(got))
	}
}
func TestMenuConvertsGeneratedDisplayVariableAndRejectsUnknownReferenceFields(t *testing.T) {
	in := []byte("#!ipxe\n# pxe-platform:folders-v1 e30\nmenu Keep pxe_fw text ${pxe_fw}\n")
	got, err := ConvertMenu(in, Replacements{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Keep pxe_fw text ${omb_fw}") {
		t.Fatal(string(got))
	}
	marker := base64.RawURLEncoding.EncodeToString([]byte(`[{"id":1,"profile_id":2,"sha256":"a","unknown_reference":"secret"}]`))
	if _, err := ConvertMenu([]byte("#!ipxe\n# pxe-platform:winanswers-v1 "+marker+"\n"), Replacements{Answers: map[int64]AnswerChange{1: {ProfileID: 2, OldHash: "a", NewHash: "b"}}}); err == nil {
		t.Fatal("discarded unknown reference field")
	}
}
func TestMenuRejectsMissingAssetWithoutMetadataAndUnsupportedCommandReference(t *testing.T) {
	r := Replacements{Profiles: map[int64]bool{1: true}, Answers: map[int64]AnswerChange{}, Drivers: map[int64]DriverChange{}}
	for _, line := range []string{"initrd http://host/assets/windows-answers/99/pxe-answer.xml pxe-answer.xml", "chain http://host/assets/windows-profiles/1/pxe-setup.cmd"} {
		if _, err := ConvertMenu([]byte("#!ipxe\n# pxe-platform:folders-v1 e30\n"+line+"\n"), r); err == nil {
			t.Fatal("accepted unknown reference", line)
		}
	}
}
