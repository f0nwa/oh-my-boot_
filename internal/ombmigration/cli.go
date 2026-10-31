package ombmigration

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func RunCLI(ctx context.Context, args []string, out, errOut io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(errOut, "omb-migrate-data:", err); return 1 }
	if len(args) == 0 {
		return fail(fmt.Errorf("usage: omb-migrate-data plan|apply|verify --manifest FILE"))
	}
	mode := args[0]
	if mode != "plan" && mode != "apply" && mode != "verify" {
		return fail(fmt.Errorf("unknown migration mode"))
	}
	flags := flag.NewFlagSet("omb-migrate-data "+mode, flag.ContinueOnError)
	flags.SetOutput(errOut)
	manifest := flags.String("manifest", "", "private source inventory JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return 1
	}
	if *manifest == "" || flags.NArg() != 0 {
		return fail(fmt.Errorf("exactly one --manifest FILE is required"))
	}
	if os.Geteuid() != 0 {
		return fail(fmt.Errorf("run as root for private data and peer PostgreSQL access"))
	}
	p, err := filepath.Abs(*manifest)
	if err != nil {
		return fail(err)
	}
	if _, err = safePath("/", p); err != nil {
		return fail(err)
	}
	b, err := privateRead(p)
	if err != nil {
		return fail(fmt.Errorf("cannot read private source manifest"))
	}
	m, err := ParseManifest(b)
	if err != nil {
		return fail(err)
	}
	runner := ExecRunner{}
	engine := &Engine{Root: "/", ManifestPath: p, Manifest: m, DB: PSQL{Runner: runner}, Rebuilder: RuntimeRebuilder{Runner: runner}, ServicesStopped: func(c context.Context) error { return CheckStopped(c, runner, m.Profiles) }}
	var report Report
	switch mode {
	case "plan":
		renamed := false
		if _, e := os.Lstat(p + ".state.json"); e == nil {
			state, e := engine.state()
			if e != nil {
				return fail(e)
			}
			renamed = state["system_complete"] == true
		}
		report, err = engine.Plan(ctx, renamed)
	case "apply":
		report, err = engine.Apply(ctx)
	case "verify":
		report, err = engine.Verify(ctx)
	}
	if err != nil {
		return fail(err)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err != nil {
		return fail(err)
	}
	return 0
}
func CheckStopped(ctx context.Context, runner Runner, profiles []Object) error {
	units := []string{}
	for _, brand := range []string{"pxe", "omb"} {
		for _, service := range []string{"boot", "control", "images", "agent", "dnsmasq"} {
			units = append(units, brand+"-"+service+".service")
		}
		for _, profile := range profiles {
			units = append(units, brand+"-winiso@"+strconv.FormatInt(number(profile, "id"), 10)+".service")
		}
	}
	args := append([]string{"show", "--property=ActiveState", "--value", "--"}, units...)
	b, err := runner.Run(ctx, "systemctl", args, nil)
	if err != nil {
		return fmt.Errorf("cannot verify stopped application services")
	}
	states := strings.Fields(string(b))
	if len(states) != len(units) {
		return fmt.Errorf("incomplete service state evidence")
	}
	for i, state := range states {
		if state != "inactive" && state != "failed" {
			return fmt.Errorf("application writer still active: %s", units[i])
		}
	}
	return nil
}
