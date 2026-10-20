package boot

import (
	"context"
	"io"
	"log"
	"testing"
	"time"
)

const sampleARP = `IP address       HW type     Flags       HW address            Mask     Device
10.30.0.19       0x1         0x2         bc:24:11:04:2a:ed     *        ens19
10.30.0.20       0x1         0x0         00:00:00:00:00:00     *        ens19
192.168.10.10    0x1         0x2         aa:bb:cc:dd:ee:ff     *        ens18
`

func TestNeighborMACFromARPTable(t *testing.T) {
	for ip, want := range map[string]string{"10.30.0.19": "bc:24:11:04:2a:ed", "10.30.0.20": "", "10.30.0.99": "", "192.168.10.10": "aa:bb:cc:dd:ee:ff"} {
		if got := neighborMAC(sampleARP, ip); got != want {
			t.Errorf("%s: got %q want %q", ip, got, want)
		}
	}
}

type captureStore struct{ events chan Event }

func (s captureStore) Insert(_ context.Context, e Event) error {
	s.events <- e
	return nil
}

func TestRecorderAddsMACFromNeighbors(t *testing.T) {
	store := captureStore{events: make(chan Event, 1)}
	recorder := NewRecorder(store, log.New(io.Discard, "", 0))
	recorder.lookupMAC = func(ip string) string {
		if ip == "10.30.0.19" {
			return "bc:24:11:04:2a:ed"
		}
		return ""
	}
	recorder.Record(Event{Kind: "menu", Path: "/boot/menu.ipxe", RemoteAddr: "10.30.0.19:51234", At: time.Now()})
	select {
	case e := <-store.events:
		if e.MAC != "bc:24:11:04:2a:ed" {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event not stored")
	}
	_ = recorder.Close(context.Background())
}
