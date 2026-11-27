package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const statusFilename = "status.json"

type Release struct {
	CurrentCommit string `json:"current_commit,omitempty"`
	Current       string `json:"current,omitempty"`
	Target        string `json:"target,omitempty"`
	Channel       string `json:"channel,omitempty"`
	Commit        string `json:"commit,omitempty"`
	Services      string `json:"services,omitempty"`
}

type Status struct {
	ID      string  `json:"id,omitempty"`
	State   string  `json:"state"`
	Phase   string  `json:"phase,omitempty"`
	Reason  string  `json:"reason,omitempty"`
	Release Release `json:"release"`
}

func loadStatus(dir string) (Status, error) {
	data, err := os.ReadFile(filepath.Join(dir, statusFilename))
	if os.IsNotExist(err) {
		return Status{State: "idle"}, nil
	}
	if err != nil {
		return Status{}, err
	}
	var status Status
	err = json.Unmarshal(data, &status)
	return status, err
}

func writeStatus(dir string, status Status) error {
	file, err := os.CreateTemp(dir, ".status-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := json.NewEncoder(file).Encode(status); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, statusFilename))
}
