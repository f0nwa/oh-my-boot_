package unattend

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const nsWLAN = "http://www.microsoft.com/networking/WLAN/profile/v1"

var wlanNS = map[string]string{"w": nsWLAN}

// wifiProfile is IProfileWifiSettings.
type wifiProfile struct {
	doc                  *Document
	name                 string
	connectAutomatically bool
}

func profileFromXML(s XmlWifiSettings) wifiProfile {
	doc, err := ParseXML([]byte(s.Xml))
	if err != nil {
		// The original also validates against WLAN_profile_v1.xsd; the port
		// checks well-formedness only (see UPSTREAM.md).
		fail("WLAN profile XML is invalid: %s", err.Error())
	}
	p := wifiProfile{doc: doc}
	if el := doc.SelectNode("/w:WLANProfile/w:connectionMode", wlanNS); el != nil {
		p.connectAutomatically = el.InnerText() == "auto"
	}
	p.name = doc.MustSelectNode("/w:WLANProfile/w:name", wlanNS).InnerText()
	return p
}

func profileFromParameters(s ParameterizedWifiSettings) wifiProfile {
	doc := resourceXML("WLANProfile.xml")
	at := func(path string) *Node { return doc.MustSelectNode(path, wlanNS) }
	at("/w:WLANProfile/w:name").SetInnerText(s.Name)
	at("/w:WLANProfile/w:SSIDConfig/w:SSID/w:name").SetInnerText(s.Name)
	at("/w:WLANProfile/w:SSIDConfig/w:SSID/w:hex").SetInnerText(strings.ToUpper(hex.EncodeToString([]byte(s.Name))))
	at("/w:WLANProfile/w:connectionType").SetInnerText("ESS")
	mode := "manual"
	if s.ConnectAutomatically {
		mode = "auto"
	}
	at("/w:WLANProfile/w:connectionMode").SetInnerText(mode)
	auth := func(a, e string) {
		at("/w:WLANProfile/w:MSM/w:security/w:authEncryption/w:authentication").SetInnerText(a)
		at("/w:WLANProfile/w:MSM/w:security/w:authEncryption/w:encryption").SetInnerText(e)
	}
	password := func() {
		at("/w:WLANProfile/w:MSM/w:security/w:sharedKey/w:keyMaterial").SetInnerText(s.Password)
	}
	nonBroadcast := func() {
		if s.NonBroadcast {
			el := NewElement(nsWLAN, "nonBroadcast")
			el.SetInnerText("true")
			at("/w:WLANProfile/w:SSIDConfig").AppendChild(el)
		}
	}
	switch s.Authentication {
	case "Open":
		auth("open", "none")
		nonBroadcast()
		at("/w:WLANProfile/w:MSM/w:security/w:sharedKey").RemoveSelf()
	case "WPA2PSK":
		auth("WPA2PSK", "AES")
		nonBroadcast()
		password()
	case "WPA3SAE":
		auth("WPA3SAE", "AES")
		nonBroadcast()
		password()
		el := NewElement("http://www.microsoft.com/networking/WLAN/profile/v4", "transitionMode")
		el.SetInnerText("true")
		at("/w:WLANProfile/w:MSM/w:security/w:authEncryption").AppendChild(el)
	default:
		panic(fmt.Sprintf("Authentication mode %s is not implemented.", s.Authentication))
	}
	return wifiProfile{doc: doc, name: s.Name, connectAutomatically: s.ConnectAutomatically}
}

func wifiModifier(m *modifierContext) {
	el := m.must("//u:OOBE/u:HideWirelessSetupInOOBE")
	var p wifiProfile
	switch s := m.config.WifiSettings.(type) {
	case InteractiveWifiSettings:
		el.SetInnerText("false")
		return
	case SkipWifiSettings:
		el.SetInnerText("true")
		return
	case XmlWifiSettings:
		p = profileFromXML(s)
	case ParameterizedWifiSettings:
		p = profileFromParameters(s)
	default:
		panic(fmt.Sprintf("unsupported %T", s))
	}
	el.RemoveSelf()
	file := m.embedXMLFile("Wifi.xml", p.doc)
	m.specialize.append("$name = 'WlanSvc';\n" +
		"$start = [datetime]::Now;\n" +
		"$timeout = $start.AddMinutes( 1 );\n" +
		"$params = @{\n" +
		"  Id = 1;\n" +
		"  ParentId = 0;\n" +
		"  Activity = \"Waiting for service '${name}' to start.\";\n" +
		"};\n" +
		"while( $true ) {\n" +
		"\tif( $service = Get-Service -Name $name -ErrorAction 'SilentlyContinue' ) {\n" +
		"\t\tif( $service.Status -eq 'Running' ) {\n" +
		"\t\t\tbreak;\n" +
		"\t\t}\n" +
		"\t}\n" +
		"\tif( [datetime]::Now -gt $timeout ) {\n" +
		"\t\t\"Service '${name}' did not start in time.\" | Write-Warning;\n" +
		"\t\tbreak;\n" +
		"\t}\n" +
		"\tWrite-Progress @params -PercentComplete $(\n" +
		"\t\t100 * ( [datetime]::Now - $start ).Ticks / ( $timeout - $start ).Ticks\n" +
		"\t);\n" +
		"\tStart-Sleep -Seconds 5;\n" +
		"}\n" +
		"Write-Progress @params -Completed;")
	m.specialize.append(`netsh.exe wlan add profile filename="` + file + `" user=all;`)
	if p.connectAutomatically {
		m.specialize.append(`netsh.exe wlan connect name="` + p.name + `" ssid="` + p.name + `";`)
	}
}
