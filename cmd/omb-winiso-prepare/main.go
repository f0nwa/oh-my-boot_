package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"omb/internal/agent"
)

func main() {
	if len(os.Args) != 2 {
		fatal(fmt.Errorf("usage: omb-winiso-prepare PROFILE_ID"))
	}
	id, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != os.Args[1] {
		fatal(fmt.Errorf("invalid profile ID"))
	}
	req, err := readProfileRequest(filepath.Join("/etc/omb/winiso", os.Args[1]+".json"), id)
	if err != nil {
		fatal(err)
	}
	template, err := agent.ResolveWinPETemplate(agent.CustomWinPETemplatePath, agent.DefaultWinPETemplatePath)
	if err != nil {
		fatal(err)
	}
	winpeshl, err := os.ReadFile("/usr/share/omb/windows-profiles/winpeshl.ini")
	if err != nil {
		fatal(err)
	}
	secret, err := os.ReadFile("/etc/omb/omb-iso-password")
	if err != nil {
		fatal(err)
	}
	runtime := agent.WindowsISORuntime{
		SourceRoot:  "/mnt/omb-smb",
		LocalRoot:   agent.LocalISORoot,
		MountRoot:   "/mnt/omb-iso/profiles",
		AssetRoot:   "/srv/omb/tftp/windows-profiles",
		Template:    template,
		Winpeshl:    winpeshl,
		Password:    strings.TrimSpace(string(secret)),
		Mounter:     agent.CommandWindowsISOMounter{},
		WIMCommands: agent.ExecDriverCommandRunner{},
	}
	if _, err := runtime.Prepare(context.Background(), req); err != nil {
		fatal(err)
	}
}

func readProfileRequest(configPath string, id int64) (agent.WindowsISORequest, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return agent.WindowsISORequest{}, err
	}
	if len(content) > 4096 {
		return agent.WindowsISORequest{}, fmt.Errorf("Windows ISO profile config is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var req agent.WindowsISORequest
	if err := decoder.Decode(&req); err != nil {
		return agent.WindowsISORequest{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return agent.WindowsISORequest{}, fmt.Errorf("extra data in Windows ISO profile config")
	}
	if req.ID != id {
		return agent.WindowsISORequest{}, fmt.Errorf("Windows ISO profile ID mismatch")
	}
	return req, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
