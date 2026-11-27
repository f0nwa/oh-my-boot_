package update

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type LocalInstaller struct {
	Script        string
	PublicKey     string
	Address       string
	DeploymentDir string
}

func (i LocalInstaller) Check(ctx context.Context, repo string) (Release, error) {
	cmd := exec.CommandContext(ctx, i.Script, "check-update", repo, i.PublicKey, i.Address)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return Release{}, err
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 6 {
		return Release{}, errors.New("unexpected preflight output")
	}
	return Release{
		Current: fields[0], Target: fields[1], Channel: fields[2],
		Commit: fields[3], Services: fields[4], CurrentCommit: fields[5],
	}, nil
}

func (i LocalInstaller) Apply(repo string, release Release, progress func(string)) error {
	cmd := exec.Command(i.Script, "update", repo, i.PublicKey, i.Address)
	cmd.Env = append(os.Environ(),
		"OMB_UPDATE_PROGRESS=1",
		"OMB_UPDATE_EXPECT_CURRENT_VERSION="+release.Current,
		"OMB_UPDATE_EXPECT_CURRENT_COMMIT="+release.CurrentCommit,
	)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		line := scanner.Text()
		if phase, ok := strings.CutPrefix(line, "OMB_UPDATE_PHASE="); ok {
			switch phase {
			case "backup", "apply", "verify", "rollback", "rolled_back":
				progress(phase)
			}
		} else {
			fmt.Fprintln(os.Stdout, line)
		}
	}
	if scanner.Err() != nil {
		_ = out.Close()
	}
	return errors.Join(scanner.Err(), cmd.Wait())
}

func (i LocalInstaller) Pending() (bool, error) {
	_, err := os.Stat(filepath.Join(i.DeploymentDir, "PENDING"))
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (i LocalInstaller) Committed(release Release) bool {
	version, versionErr := os.ReadFile(filepath.Join(i.DeploymentDir, "PACKAGE"))
	commit, commitErr := os.ReadFile(filepath.Join(i.DeploymentDir, "COMMIT"))
	return versionErr == nil && commitErr == nil &&
		strings.TrimSpace(string(version)) == release.Target &&
		strings.TrimSpace(string(commit)) == release.Commit
}
