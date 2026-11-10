package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const sampleJournal = `{"__REALTIME_TIMESTAMP":"1792268938244731","SYSLOG_IDENTIFIER":"dnsmasq","MESSAGE":"dnsmasq-dhcp: PXE(ens19) bc:24:11:52:02:39 proxy"}
{"__REALTIME_TIMESTAMP":"1792268938251452","SYSLOG_IDENTIFIER":"dnsmasq-dhcp","MESSAGE":"PXE(ens19) bc:24:11:52:02:39 proxy"}
{"__REALTIME_TIMESTAMP":"1792268939000000","SYSLOG_IDENTIFIER":"dnsmasq-dhcp","MESSAGE":"DHCPACK(ens19) 10.30.0.150 52:54:00:12:34:56 lab-pc"}
{"__REALTIME_TIMESTAMP":"1792268940000000","SYSLOG_IDENTIFIER":"dnsmasq-tftp","MESSAGE":"sent /srv/omb/tftp/ipxeboot/x86_64-sb/ipxe-shim.efi to 10.30.0.19"}
{"__REALTIME_TIMESTAMP":"1792268941000000","SYSLOG_IDENTIFIER":"dnsmasq-tftp","MESSAGE":"failed sending /srv/omb/tftp/ipxeboot/x86_64/undionly.kpxe to 10.30.0.20"}
{"__REALTIME_TIMESTAMP":"1792268942000000","SYSLOG_IDENTIFIER":"dnsmasq-tftp","MESSAGE":"TFTP root is /srv/omb/tftp"}
not json
`

func TestParseDnsmasqJournal(t *testing.T) {
	events := ParseDnsmasqJournal(strings.NewReader(sampleJournal))
	if len(events) != 4 {
		t.Fatalf("events=%+v", events)
	}
	// newest first
	want := []DHCPEvent{
		{Kind: "tftp-failed", IP: "10.30.0.20", Detail: "ipxeboot/x86_64/undionly.kpxe"},
		{Kind: "tftp-sent", IP: "10.30.0.19", Detail: "ipxeboot/x86_64-sb/ipxe-shim.efi"},
		{Kind: "DHCPACK", IP: "10.30.0.150", MAC: "52:54:00:12:34:56", Detail: "lab-pc"},
		{Kind: "PXE", MAC: "bc:24:11:52:02:39", Detail: "proxy"},
	}
	for i, w := range want {
		got := events[i]
		got.At = time.Time{}
		if got != w {
			t.Errorf("%d: got %+v want %+v", i, got, w)
		}
	}
	if events[3].At.Unix() != 1792268938 {
		t.Errorf("time %v", events[3].At)
	}
}

func TestDHCPEventsReuseSnapshot(t *testing.T) {
	var reads atomic.Int32
	log := &DHCPLog{Read: func(context.Context) ([]byte, error) {
		reads.Add(1)
		return []byte(sampleJournal), nil
	}}
	h := HandlerWithDHCPLog(http.NotFoundHandler(), log)
	for range 3 {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/dhcp-events", nil))
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "tftp-failed") {
			t.Fatalf("snapshot response: %d %s", res.Code, res.Body)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("repeated source scans: got %d, want 1", reads.Load())
	}
}

func TestDHCPEventsEndpoint(t *testing.T) {
	log := &DHCPLog{Read: func(context.Context) ([]byte, error) { return []byte(sampleJournal), nil }}
	res := httptest.NewRecorder()
	HandlerWithDHCPLog(http.NotFoundHandler(), log).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/dhcp-events", nil))
	var events []DHCPEvent
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &events) != nil || len(events) != 4 {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	other := httptest.NewRecorder()
	HandlerWithDHCPLog(http.NotFoundHandler(), log).ServeHTTP(other, httptest.NewRequest(http.MethodGet, "/status", nil))
	if other.Code != http.StatusNotFound {
		t.Fatalf("inner handler not used: %d", other.Code)
	}
}
