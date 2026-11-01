package autoinstall

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestStaticDesktopNetwork(t *testing.T) {
	f := validForm()
	f.Network = NetworkValues{Mode: "static", MAC: "52:54:00:12:34:56", Address: "192.0.2.10/24", Gateway: "192.0.2.1", DNS: []string{"192.0.2.53"}}
	_, data, err := Compile(f)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	yaml.Unmarshal(data, &doc)
	n := doc["autoinstall"].(map[string]any)["network"].(map[string]any)
	e := n["ethernets"].(map[string]any)["omb0"].(map[string]any)
	if n["renderer"] != "NetworkManager" || e["dhcp4"] != false || e["match"].(map[string]any)["macaddress"] != f.Network.MAC {
		t.Fatalf("network: %#v", n)
	}
	for _, mutate := range []func(*Form){func(f *Form) { f.Network.MAC = "ff:ff:ff:ff:ff:ff" }, func(f *Form) { f.Network.Address = "::1/64" }, func(f *Form) { f.Network.Gateway = "garbage" }, func(f *Form) { f.Network.DNS = []string{"invalid"} }} {
		x := f
		mutate(&x)
		if _, _, e := Compile(x); e == nil {
			t.Fatal("invalid network accepted")
		}
	}
}
