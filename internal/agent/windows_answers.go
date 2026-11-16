package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type WindowsAnswerRequest struct {
	ID     int64  `json:"id"`
	SHA256 string `json:"sha256"`
	XML    []byte `json:"xml"`
}

type WindowsAnswerCheck struct {
	ID     int64  `json:"id"`
	SHA256 string `json:"sha256"`
}

func (m *WindowsISOManager) PrepareAnswer(_ context.Context, req WindowsAnswerRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	check := WindowsAnswerCheck{ID: req.ID, SHA256: req.SHA256}
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
	if err := os.MkdirAll(m.AnswerRoot, 0750); err != nil {
		return err
	}
	root, err := os.Lstat(m.AnswerRoot)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("answer asset root is unsafe")
	}
	final := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	if _, err := os.Lstat(final); err == nil {
		return m.checkAnswerAssets(check)
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(m.AnswerRoot, ".answer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "omb-answer.xml"), req.XML, 0640); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0750); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	return m.checkAnswerAssets(check)
}

func (m *WindowsISOManager) CheckAnswer(_ context.Context, req WindowsAnswerCheck) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validWindowsAnswerRequest(req); err != nil {
		return err
	}
	return m.checkAnswerAssets(req)
}

func validWindowsAnswerRequest(req WindowsAnswerCheck) error {
	if req.ID <= 0 || len(req.SHA256) != 64 {
		return fmt.Errorf("invalid answer asset identity")
	}
	for _, c := range req.SHA256 {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return fmt.Errorf("invalid answer asset hash")
		}
	}
	return nil
}

func (m *WindowsISOManager) checkAnswerAssets(req WindowsAnswerCheck) error {
	final := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	info, err := os.Lstat(final)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("answer asset directory is unavailable")
	}
	if err := requireRegularISOFile(final, "omb-answer.xml"); err != nil {
		return err
	}
	xml, err := os.ReadFile(filepath.Join(final, "omb-answer.xml"))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(xml)
	if hex.EncodeToString(sum[:]) != req.SHA256 {
		return fmt.Errorf("answer asset hash differs")
	}
	return nil
}

var (
	errAnswerMissing     = errors.New("answer file is missing")
	errAnswerHashDiffers = errors.New("answer file hash differs")
)

// AnswerXML returns the stored answer file of a variant, for the editor. The
// hash must match, so Control gets the file it knows, not another one.
func (m *WindowsISOManager) AnswerXML(_ context.Context, req WindowsAnswerCheck) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validWindowsAnswerRequest(req); err != nil {
		return nil, err
	}
	final := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	info, err := os.Lstat(final)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errAnswerMissing
	}
	if err := requireRegularISOFile(final, "omb-answer.xml"); err != nil {
		return nil, errAnswerMissing
	}
	xml, err := os.ReadFile(filepath.Join(final, "omb-answer.xml"))
	if err != nil {
		return nil, errAnswerMissing
	}
	sum := sha256.Sum256(xml)
	if hex.EncodeToString(sum[:]) != req.SHA256 {
		return nil, errAnswerHashDiffers
	}
	return xml, nil
}
