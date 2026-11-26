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
	nameAt := strings.Index(page, `>Windows-new.iso</button>`)
	if nameAt < 0 {
		t.Fatal("missing image row")
	}
	start := strings.LastIndex(page[:nameAt], "<tr>")
	if start < 0 {
		t.Fatal("missing start of image row")
	}
	row := page[start:]
	end := strings.Index(row, "</tr>")
	if end < 0 {
		t.Fatal("missing end of image row")
	}
	row = row[:end]
	lastCell := row[strings.LastIndex(row, "<td"):]
	if !strings.Contains(lastCell, `action="/images/iso/inspect"`) || !strings.Contains(lastCell, `>Создать загрузку</button>`) || !strings.Contains(lastCell, `name="mode" value="inspect"`) {
		t.Fatalf("inspect and create actions must appear in the last cell: %s", row)
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
