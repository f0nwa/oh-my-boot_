package control

import (
	"strings"
	"testing"
)

func TestImageActionsAreInTheLastColumn(t *testing.T) {
	s, _, _ := downloadsServer(t)
	page := libraryGet(t, s, "/images")
	if strings.Contains(page, `href="/installations/new">Создать загрузку</a>`) {
		t.Fatal("unexpected shared create action below the list")
	}
	start := strings.Index(page, `<td class="path-cell">Windows-new.iso`)
	if start < 0 {
		t.Fatal("missing image row")
	}
	row := page[start:]
	end := strings.Index(row, "</tr>")
	if end < 0 {
		t.Fatal("missing end of image row")
	}
	row = row[:end]
	action := strings.Index(row, `action="/images/iso/inspect"`)
	if action < 0 || action < strings.LastIndex(row, "<td") || strings.Count(row, `action="/images/iso/inspect"`) != 1 {
		t.Fatalf("image action must appear once, in the last cell: %s", row)
	}
}

func TestLibraryOmitsUSBActionForPreparedImages(t *testing.T) {
	s, store, _ := downloadsServer(t)
	store.profiles[0].State = "ready"
	store.profiles[0].InstallFile = "install.wim"
	page := libraryGet(t, s, "/images")
	if strings.Contains(page, `href="/usb?profile_id=`) {
		t.Fatal("ISO rows should not contain USB actions")
	}
}
