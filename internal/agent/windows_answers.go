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
	parent := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	if err := os.MkdirAll(parent, 0750); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("answer asset directory is unsafe")
	}
	final := filepath.Join(parent, req.SHA256)
	if _, err := os.Lstat(final); err == nil {
		return m.checkAnswerAssets(check)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := stageVersionedAnswer(parent, req.SHA256, req.XML); err != nil {
		return err
	}
	return m.checkAnswerAssets(check)
}

func stageVersionedAnswer(parent, digest string, xml []byte) error {
	final := filepath.Join(parent, digest)
	stage, err := os.MkdirTemp(parent, ".answer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.WriteFile(filepath.Join(stage, "omb-answer.xml"), xml, 0640); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0750); err != nil {
		return err
	}
	if err := os.Rename(stage, final); err != nil {
		return err
	}
	return nil
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

func (m *WindowsISOManager) answerAssetDir(req WindowsAnswerCheck) (string, error) {
	parent := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errAnswerMissing
	}
	versioned := filepath.Join(parent, req.SHA256)
	if vInfo, err := os.Lstat(versioned); err == nil && vInfo.IsDir() && vInfo.Mode()&os.ModeSymlink == 0 {
		return versioned, nil
	}
	// Fallback to legacy unversioned directory (<id>/omb-answer.xml) for untouched pre-versioning answers.
	if err := requireRegularISOFile(parent, "omb-answer.xml"); err == nil {
		return parent, nil
	}
	return "", errAnswerMissing
}

func (m *WindowsISOManager) checkAnswerAssets(req WindowsAnswerCheck) error {
	final, err := m.answerAssetDir(req)
	if err != nil {
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
	parent := filepath.Join(m.AnswerRoot, strconv.FormatInt(req.ID, 10))
	if final == parent {
		// Materialize the versioned <id>/<sha256>/omb-answer.xml copy for
		// pre-v0.2.7 answers so menu republishes serve them at the versioned URL
		// while keeping the legacy <id>/omb-answer.xml intact for rollbacks.
		if err := stageVersionedAnswer(parent, req.SHA256, xml); err != nil {
			return err
		}
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
	final, err := m.answerAssetDir(req)
	if err != nil {
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
