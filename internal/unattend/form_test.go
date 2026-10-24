package unattend

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestFormRoundTrip: every golden configuration survives the form.
func TestFormRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("testdata/golden/*.json")
	d := loadTestData(t)
	var configs []Config
	for _, file := range files {
		raw, _ := os.ReadFile(file)
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		configs = append(configs, c)
	}
	for _, file := range []string{"testdata/foreign/schneegans-old.xml", "testdata/foreign/hand-made.xml"} {
		raw, _ := os.ReadFile(file)
		c, _, err := Open(d, raw)
		if err != nil {
			t.Fatal(err)
		}
		configs = append(configs, c)
	}
	for i, c := range configs {
		back, err := ParseFormValues(FormValues(c))
		if err != nil {
			t.Fatalf("%d: %v", i, err)
		}
		a, _ := json.Marshal(c)
		b, _ := json.Marshal(back)
		if string(a) != string(b) {
			t.Errorf("%d: form changed the settings\n%s\n%s", i, a, b)
		}
		if !reflect.DeepEqual(c, back) {
			t.Errorf("%d: not deeply equal", i)
		}
	}
}

func TestFormBlankRowsAndErrors(t *testing.T) {
	c := Default()
	c.AccountSettings = UnattendedAccountSettings{Accounts: []Account{{Name: "a", Group: "Administrators"}}, AutoLogonSettings: NoneAutoLogonSettings{}}
	v := FormValues(c)
	// A blank second row, as the page renders it.
	v.Set("AccountSettings.UnattendedAccountSettings.Accounts.1.Name", "")
	v.Set("AccountSettings.UnattendedAccountSettings.Accounts.1.Group", "Users")
	back, err := ParseFormValues(v)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(back.AccountSettings.(UnattendedAccountSettings).Accounts); n != 1 {
		t.Fatalf("%d accounts", n)
	}
	v.Set("PESettings.$type", "GeneratePESettings")
	v.Set("PESettings.GeneratePESettings.PartitionSettings.UnattendedPartitionSettings.SystemSize", "много")
	var fe *FormError
	if _, err := ParseFormValues(v); !errors.As(err, &fe) || fe.Problem != "number" {
		t.Fatalf("want a number error, got %v", err)
	}
	v.Set("LanguageSettings.$type", "Nonsense")
	if _, err := ParseFormValues(v); !errors.As(err, &fe) || fe.Problem != "choice" {
		t.Fatalf("want a choice error, got %v", err)
	}
}
