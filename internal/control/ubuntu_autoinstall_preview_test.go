package control

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"omb/internal/autoinstall"
	"strings"
	"testing"
)

func TestUbuntuPreviewReusesOnlyOwnValidHash(t *testing.T) {
	s, store := autoinstallServer(t)
	form := constructorForm()
	token, data := constructorPreview(t, s, form)
	form.Set("preview_token", token)
	form.Set("password", "")
	res := constructorPost(t, s, "/ubuntu-autoinstall", form)
	if res.Code != http.StatusSeeOther || len(store.files) != 1 {
		t.Fatalf("save %d %s", res.Code, res.Body.String())
	}
	if !bytes.Equal(data, store.files[0].UserData) {
		t.Fatal("saved different bytes")
	}
	form.Set("hostname", "changed")
	if constructorPost(t, s, "/ubuntu-autoinstall", form).Code != 400 {
		t.Fatal("changed form accepted")
	}
}
func TestUbuntuPreviewLargeScriptAndLimits(t *testing.T) {
	s, store := autoinstallServer(t)
	form := constructorForm()
	form.Set("post_install_script", "# "+strings.Repeat("a", 12000))
	form.Set("realname", "Desktop user")
	form.Set("packages", "curl\nvim")
	token, data := constructorPreview(t, s, form)
	if !bytes.Contains(data, []byte("curtin")) {
		t.Fatal("missing script")
	}
	form.Set("preview_token", token)
	form.Set("password", "")
	if res := constructorPost(t, s, "/ubuntu-autoinstall", form); res.Code != 303 || len(store.files) != 1 {
		t.Fatalf("save %d", res.Code)
	}
	form.Set("post_install_script", strings.Repeat("a", 600000))
	if constructorPost(t, s, "/ubuntu-autoinstall/preview", form).Code != 413 {
		t.Fatal("oversized form accepted")
	}
}

type desktopSourceStub struct{ available bool }

func (f *desktopSourceStub) UbuntuSource(context.Context) (autoinstall.SourceCatalog, error) {
	if !f.available {
		return autoinstall.SourceCatalog{}, errors.New("unavailable")
	}
	return autoinstall.SourceCatalog{ISOSHA256: strings.Repeat("a", 64), Sources: []autoinstall.SourceOption{{ID: "ubuntu-desktop"}}}, nil
}
func TestUbuntuSourceRecheckedOnSave(t *testing.T) {
	s, store := autoinstallServer(t)
	source := &desktopSourceStub{true}
	s.UbuntuSource = source
	form := constructorForm()
	form.Set("source_id", "ubuntu-desktop")
	token, _ := constructorPreview(t, s, form)
	form.Set("preview_token", token)
	source.available = false
	if res := constructorPost(t, s, "/ubuntu-autoinstall", form); res.Code != 400 || len(store.files) > 0 {
		t.Fatal("save accepted missing source")
	}
}

func TestUbuntuPreviewMultipleDefaultKeyboardVariants(t *testing.T) {
	s, store := autoinstallServer(t)
	f := constructorForm()
	f["keyboard_layout"] = []string{"us", "ru", "", ""}
	f["keyboard_layout_variant"] = []string{"", "", "", ""}
	token, data := constructorPreview(t, s, f)
	if !bytes.Contains(data, []byte("variant: ','")) && !bytes.Contains(data, []byte("variant: \",\"")) {
		t.Fatalf("missing aligned default variants: %s", data)
	}
	f.Set("preview_token", token)
	f.Set("password", "")
	res := constructorPost(t, s, "/ubuntu-autoinstall", f)
	if res.Code != 303 || len(store.files) != 1 || store.files[0].Values.KeyboardVariant != "," || !bytes.Equal(data, store.files[0].UserData) {
		t.Fatalf("multi-layout save failed: %d", res.Code)
	}
}
