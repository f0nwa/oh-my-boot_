package unattend

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestGoldenAll compares the output with that of the original library for
// every testdata/golden/*.json: X.xml byte for byte, or X.err as the message.
func TestGoldenAll(t *testing.T) {
	files, _ := filepath.Glob("testdata/golden/*.json")
	if len(files) == 0 {
		t.Fatal("no golden files")
	}
	d, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var c Config
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatalf("config: %v", err)
			}
			got, genErr := GenerateBytes(d, c)
			base := strings.TrimSuffix(file, ".json")
			if want, err := os.ReadFile(base + ".err"); err == nil {
				if genErr == nil {
					t.Fatalf("want error %q, got XML", want)
				}
				if genErr.Error() != strings.TrimSpace(string(want)) {
					t.Fatalf("error\n got: %s\nwant: %s", genErr, want)
				}
				return
			}
			if genErr != nil {
				t.Fatalf("generate: %v", genErr)
			}
			want, err := os.ReadFile(base + ".xml")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal(firstDiff(got, want))
			}
		})
	}
}

func firstDiff(got, want []byte) string {
	g := strings.Split(string(got), "\n")
	w := strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return "line " + itoa(i+1) + "\n got: " + strings.TrimRight(gl, "\r") + "\nwant: " + strings.TrimRight(wl, "\r")
		}
	}
	return "outputs differ"
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// TestConfigJSONRoundTrip writes every golden config out and reads it back.
func TestConfigJSONRoundTrip(t *testing.T) {
	files, _ := filepath.Glob("testdata/golden/*.json")
	for _, file := range append(files, "") {
		var c Config
		if file == "" {
			c = Default()
		} else {
			raw, _ := os.ReadFile(file)
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
		}
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("%s: marshal: %v", file, err)
		}
		var back Config
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatalf("%s: unmarshal: %v\n%s", file, err, out)
		}
		if !reflect.DeepEqual(c, back) {
			t.Fatalf("%s: round trip changed the config\n%s", file, out)
		}
	}
}

func TestUnknownSetting(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"NoSuchSetting":true}`), &c); err == nil {
		t.Fatal("want an error for an unknown setting")
	}
}

func TestParseXMLRejectsMalformed(t *testing.T) {
	for _, s := range []string{"<a>", "<a></b>", "<a><b></a></b>"} {
		if _, err := ParseXML([]byte(s)); err == nil {
			t.Errorf("%q: want an error", s)
		}
	}
}

// TestPEScriptFindsPXEImage pins what winpe.RenderAnswerStartup relies on:
// pe.cmd looks for the image on I: and for the answer file on X:.
func TestPEScriptFindsPXEImage(t *testing.T) {
	d, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.PESettings = FormDefault(reflect.TypeOf(GeneratePESettings{})).Interface().(GeneratePESettings)
	doc, err := Generate(d, c)
	if err != nil {
		t.Fatal(err)
	}
	script := doc.MustSelectNode("//s:PEScriptCopy", namespaces).InnerText()
	for _, want := range []string{
		"for %%d in (C D E F G H I J K L M N O P Q T U V X Y Z) do (",
		`if exist %%d:\sources\install.wim set "IMAGE_FILE=%%d:\sources\install.wim"`,
		`if exist %%d:\autounattend.xml set "XML_FILE=%%d:\autounattend.xml"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("pe.cmd lacks %q", want)
		}
	}
}
