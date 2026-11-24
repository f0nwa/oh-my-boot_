package control

import (
	"context"
	"testing"
)

func TestNetworkDriverStore(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	mac := "a4:bb:6d:14:cb:f7"
	// The canonical schema supports a clean installation immediately.
	var driver string
	if _, err := store.AddMachine(ctx, mac, MachineEdit{Name: "preserved"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT network_driver FROM machines WHERE mac=$1`, mac).Scan(&driver); err != nil || driver != "" {
		t.Fatalf("fresh default=%q %v", driver, err)
	}
	if _, err := store.AddMacRule(ctx, MacRule{Prefix: "a4:bb:6d", Title: "preserved rule"}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"machines", "mac_prefix_rules"} {
		if err := store.Pool.QueryRow(ctx, "SELECT network_driver FROM "+table).Scan(&driver); err != nil || driver != "" {
			t.Fatalf("%s migrated default=%q %v", table, driver, err)
		}
		if _, err := store.Pool.Exec(ctx, "UPDATE "+table+" SET network_driver='other'"); err == nil {
			t.Fatalf("%s accepted invalid driver", table)
		}
	}
	if err := store.SetMachineNetworkDriver(ctx, mac, "snp"); err != nil {
		t.Fatal(err)
	}
	if err := store.EditMachine(ctx, mac, MachineEdit{Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMachineImport(ctx, []machineImportChange{{MAC: mac, Edit: MachineEdit{Name: "imported"}}}); err != nil {
		t.Fatal(err)
	}
	if added, err := store.AddMachine(ctx, mac, MachineEdit{}); err != nil || added {
		t.Fatalf("duplicate add=%v %v", added, err)
	}
	machines, err := store.Machines(ctx)
	if err != nil || len(machines) != 1 || machines[0].NetworkDriver != "snp" || machines[0].Name != "imported" {
		t.Fatalf("machines=%+v %v", machines, err)
	}
	if err := store.EditMacRule(ctx, MacRule{Prefix: "a4:bb:6d", Title: "preserved rule", NetworkDriver: "ipxe"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMacRule(ctx, MacRule{Prefix: "bc:24:11", Title: "empty"}); err != nil {
		t.Fatal(err)
	}
	rules, err := store.MacRules(ctx)
	if err != nil || len(rules) != 2 || rules[0].NetworkDriver != "ipxe" {
		t.Fatalf("rules=%+v %v", rules, err)
	}
	snapshot, err := store.BootDriverRules(ctx)
	if err != nil || len(snapshot) != 2 {
		t.Fatalf("snapshot=%+v %v", snapshot, err)
	}
	found := map[string]string{}
	for _, rule := range snapshot {
		found[rule.Match] = rule.Driver
	}
	if found[mac] != "snp" || found["a4:bb:6d"] != "ipxe" {
		t.Fatal(found)
	}
	if err := store.SetMachineNetworkDriver(ctx, "bc:24:11:00:00:01", "snp"); err == nil {
		t.Fatal("unknown machine silently accepted")
	}
}
