package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"pxe-platform/internal/winpe"
)

type WindowsAnswerRequest struct {
	ID        int64  `json:"id"`
	ProfileID int64  `json:"profile_id"`
	SHA256    string `json:"sha256"`
	XML       []byte `json:"xml"`
}

type WindowsAnswerCheck struct {
	ID        int64  `json:"id"`
	ProfileID int64  `json:"profile_id"`
	SHA256    string `json:"sha256"`
}

func (m *WindowsISOManager) PrepareAnswer(_ context.Context, req WindowsAnswerRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	check := WindowsAnswerCheck{ID: req.ID, ProfileID: req.ProfileID, SHA256: req.SHA256}
	if err := validWindowsAnswerRequest(check); err != nil {
		return err
	}
	if len(req.XML) == 0 || len(req.XML) > 1<<20 {
		return fmt.Errorf("invalid answer file size")
	}
	sum := sha256.Sum256(req.XML)
	if hex.EncodeToString(sum[:]) != req.SHA256 {
		return fmt.Errorf("answer file hash differs")
	}
	script, err := m.answerStartup(req.ProfileID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.AnswerRoot, 0750); err != nil {
		return err
	}
	root, err := os.Lstat(m.AnswerRoot)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("answer asset root is unsafe")
	}
	final := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	if _, err := os.Lstat(final); err == nil {
		return m.checkAnswerAssets(check, script)
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(m.AnswerRoot, ".answer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "pxe-answer.xml"), req.XML, 0640); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "pxe-setup.cmd"), script, 0640); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0750); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	return m.checkAnswerAssets(check, script)
}

func (m *WindowsISOManager) CheckAnswer(_ context.Context, req WindowsAnswerCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validWindowsAnswerRequest(req); err != nil {
		return err
	}
	script, err := m.answerStartup(req.ProfileID)
	if err != nil {
		return err
	}
	return m.checkAnswerAssets(req, script)
}

func validWindowsAnswerRequest(req WindowsAnswerCheck) error {
	if req.ID <= 0 || req.ProfileID <= 0 || len(req.SHA256) != 64 {
		return fmt.Errorf("invalid answer asset identity")
	}
	for _, c := range req.SHA256 {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return fmt.Errorf("invalid answer asset hash")
		}
	}
	return nil
}

func (m *WindowsISOManager) answerStartup(profileID int64) ([]byte, error) {
	parent := filepath.Join(m.AssetRoot, strconv.FormatInt(profileID, 10))
	if err := requireRegularISOFile(parent, "pxe-setup.cmd"); err != nil {
		return nil, err
	}
	base, err := os.ReadFile(filepath.Join(parent, "pxe-setup.cmd"))
	if err != nil {
		return nil, err
	}
	return winpe.RenderAnswerStartup(base, profileID)
}

func (m *WindowsISOManager) checkAnswerAssets(req WindowsAnswerCheck, expectedScript []byte) error {
	final := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	info, err := os.Lstat(final)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("answer asset directory is unavailable")
	}
	for _, name := range []string{"pxe-answer.xml", "pxe-setup.cmd"} {
		if err := requireRegularISOFile(final, name); err != nil {
			return err
		}
	}
	xml, err := os.ReadFile(filepath.Join(final, "pxe-answer.xml"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(xml)
	if hex.EncodeToString(sum[:]) != req.SHA256 {
		return fmt.Errorf("answer asset hash differs")
	}
	script, err := os.ReadFile(filepath.Join(final, "pxe-setup.cmd"))
	if err != nil {
		return err
	}
	if !bytes.Equal(script, expectedScript) {
		return fmt.Errorf("answer startup script differs")
	}
	return nil
}
