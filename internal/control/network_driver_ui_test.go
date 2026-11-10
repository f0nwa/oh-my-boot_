package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"omb/internal/network"
)

func checkDriverSelect(t *testing.T, body, name, selected string, values []string) {
	t.Helper()
	selectRE := regexp.MustCompile(`(?s)<select[^>]*name="` + name + `"[^>]*>(.*?)</select>`)
	m := selectRE.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("missing %s select", name)
	}
	options := regexp.MustCompile(`<option value="([^"]*)"([^>]*)>`).FindAllStringSubmatch(m[1], -1)
	if len(options) != len(values) {
		t.Fatalf("options=%v", options)
	}
	for i, o := range options {
		if o[1] != values[i] || strings.Contains(o[2], "selected") != (o[1] == selected) {
			t.Fatalf("option=%v selected=%q values=%v", o, selected, values)
		}
	}
}

func TestMachinePageNetworkDriver(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		for _, source := range []string{"machine", "prefix", "global"} {
			t.Run(lang+"/"+source, func(t *testing.T) {
				book := newRulesBook("operator")
				book.user.Language = lang
				mac := "bc:24:11:04:2a:ed"
				if source == "machine" {
					m := book.machines[mac]
					m.NetworkDriver = "snp"
					book.machines[mac] = m
				}
				if source == "prefix" {
					r := book.rules["bc:24:11"]
					r.NetworkDriver = "snp"
					book.rules[r.Prefix] = r
				}
				client := ueficaAgentClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/config" {
						json.NewEncoder(w).Encode(network.Config{DefaultDriver: "snp"})
					} else {
						w.Header().Set("Content-Type", "application/json")
						w.Write([]byte(`[]`))
					}
				}))
				s := &Server{Store: book, Agent: client}
				body := libraryGet(t, s, "/events/machines/"+mac)
				selected := ""
				if source == "machine" {
					selected = "snp"
				}
				checkDriverSelect(t, body, "network_driver", selected, []string{"", "ipxe", "snp"})
				labels := []string{"как у всех", "прошивки (snponly)", "глобальное значение", "машина", "префикс"}
				if lang == "en" {
					labels = []string{"inherit", "firmware (snponly)", "global default", "machine", "prefix"}
				}
				if !strings.Contains(body, labels[0]) || !strings.Contains(body, labels[1]) {
					t.Fatal("missing driver labels")
				}
				idx := 2
				if source == "machine" {
					idx = 3
				}
				if source == "prefix" {
					idx = 4
				}
				if !strings.Contains(body, labels[idx]) {
					t.Fatalf("missing source %s", labels[idx])
				}
				if source == "prefix" && !strings.Contains(body, "bc:24:11") {
					t.Fatal("missing source prefix")
				}
				editRE := regexp.MustCompile(`(?s)<form[^>]*action="/events/machines/` + mac + `/edit"[^>]*>(.*?)</form>`)
				if m := editRE.FindStringSubmatch(body); len(m) != 2 || strings.Contains(m[1], "network_driver") {
					t.Fatal("ordinary edit contains driver")
				}
				book.user.Role = "observer"
				body = libraryGet(t, s, "/events/machines/"+mac)
				if strings.Contains(body, `name="network_driver"`) || !strings.Contains(body, labels[1]) {
					t.Fatal("observer driver display/controls")
				}
			})
		}
	}
}

func TestMacRuleNetworkDriver(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		book := newRulesBook("operator")
		book.user.Language = lang
		r := book.rules["bc:24:11"]
		r.NetworkDriver = "snp"
		book.rules[r.Prefix] = r
		s := &Server{Store: book}
		body := libraryGet(t, s, "/machines")
		if !strings.Contains(body, `data-prefix="bc:24:11"`) || !strings.Contains(body, `data-driver="snp"`) {
			t.Fatal("missing rule edit button with driver")
		}
		formRE := regexp.MustCompile(`(?s)<form[^>]*id="rule-edit-form"[^>]*>(.*?)</form>`)
		m := formRE.FindStringSubmatch(body)
		if len(m) != 2 {
			t.Fatal("missing rule form")
		}
		checkDriverSelect(t, m[1], "network_driver", "", []string{"", "ipxe", "snp"})
		res := menuPost(t, s, "/machine-rules/bc:24:11", url.Values{"title": {"rule"}, "network_driver": {"ipxe"}, "q": {"laptop"}, "sort": {"name"}})
		if res.Code != 303 || book.rules["bc:24:11"].NetworkDriver != "ipxe" {
			t.Fatalf("edit=%d %+v", res.Code, book.rules)
		}
		if loc := res.Header().Get("Location"); !strings.Contains(loc, "q=laptop") || !strings.Contains(loc, "sort=name") {
			t.Fatalf("lost table context: %s", loc)
		}
		res = menuPost(t, s, "/machine-rules", url.Values{"prefix": {"00:1b:21"}, "title": {"new"}, "network_driver": {"snp"}})
		if res.Code != 303 || book.rules["00:1b:21"].NetworkDriver != "snp" {
			t.Fatalf("add=%d %+v", res.Code, book.rules)
		}
		res = menuPost(t, s, "/machine-rules", url.Values{"prefix": {"00:1b:22"}, "form": {"rule"}, "network_driver": {"snp"}})
		addRE := regexp.MustCompile(`(?s)<form[^>]*action="/machine-rules"[^>]*>(.*?)</form>`)
		m = addRE.FindStringSubmatch(res.Body.String())
		if res.Code != 400 || len(m) != 2 {
			t.Fatalf("error=%d", res.Code)
		}
		checkDriverSelect(t, m[1], "network_driver", "snp", []string{"", "ipxe", "snp"})
	}
}

func TestNetworkDefaultDriver(t *testing.T) {
	for _, lang := range []string{"ru", "en"} {
		data := samplePageData(lang)
		data.Network.DefaultDriver = "snp"
		w := httptest.NewRecorder()
		render(w, "network", data)
		checkDriverSelect(t, w.Body.String(), "default_driver", "snp", []string{"ipxe", "snp"})
		w = httptest.NewRecorder()
		render(w, "network_preview", data)
		if !strings.Contains(w.Body.String(), `name="default_driver" value="snp"`) {
			t.Fatal("preview lost default driver")
		}
		for _, role := range []string{"observer", "operator"} {
			data.Info.User.Role = role
			w = httptest.NewRecorder()
			render(w, "network", data)
			if strings.Contains(w.Body.String(), `name="default_driver"`) {
				t.Fatalf("%s got network controls", role)
			}
		}
	}
	form := url.Values{"default_driver": {"snp"}}
	req := httptest.NewRequest("POST", "/network/preview", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	cfg := networkForm(req)
	if cfg.DefaultDriver != "snp" {
		t.Fatal(cfg)
	}
	one := networkConfirmation("token", cfg)
	cfg.DefaultDriver = "ipxe"
	if networkConfirmation("token", cfg) == one {
		t.Fatal("confirmation ignores driver")
	}
	cfg = network.Config{Mode: "proxy", Interface: "test", ServerIP: "192.0.2.10", Subnet: "192.0.2.0/24", BootURL: "http://192.0.2.10/menu.ipxe", TFTPRoot: "/tmp", DefaultDriver: "bad"}
	if err := ValidateNetworkTarget(cfg, map[string]bool{"test": true}, []netip.Addr{netip.MustParseAddr("192.0.2.10")}); err == nil {
		t.Fatal("invalid default accepted")
	}
}
