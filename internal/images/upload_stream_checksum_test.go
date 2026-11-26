package images

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateChecksum(t *testing.T) {
	if algo, val, err := ValidateChecksum("", ""); err != nil || algo != "" || val != "" {
		t.Fatalf("empty checksum: got (%q,%q,%v)", algo, val, err)
	}
	if _, _, err := ValidateChecksum("", "deadbeef"); err == nil {
		t.Fatalf("expected error for value without algo")
	}
	if _, _, err := ValidateChecksum("sha1", strings.Repeat("a", 40)); err == nil {
		t.Fatalf("expected error for unsupported algo")
	}
	if _, _, err := ValidateChecksum("sha256", "nothex"); err == nil {
		t.Fatalf("expected error for short sha256")
	}
	sha := strings.Repeat("AB", 32)
	algo, val, err := ValidateChecksum(" SHA256 ", " "+sha+" ")
	if err != nil || algo != "sha256" || val != strings.ToLower(sha) {
		t.Fatalf("valid sha256: got (%q,%q,%v)", algo, val, err)
	}
}

func TestReceiveImageWithOptionsStreamingAndChecksums(t *testing.T) {
	root := realTempDir(t)
	payload := []byte("iso-stream-payload-0123456789")
	sha256Sum := sha256.Sum256(payload)
	sha512Sum := sha512.Sum512(payload)
	md5Sum := md5.Sum(payload)

	// Streaming (size = -1) with matching SHA-256
	res, err := ReceiveImageWithOptions(context.Background(), root, "", "stream-sha256.iso", ImageKindISO, bytes.NewReader(payload), -1, ReceiveOptions{
		ChecksumAlgo:  "sha256",
		ChecksumValue: hex.EncodeToString(sha256Sum[:]),
	})
	if err != nil {
		t.Fatalf("streaming sha256 failed: %v", err)
	}
	if res.Size != int64(len(payload)) || res.SHA256 != hex.EncodeToString(sha256Sum[:]) {
		t.Fatalf("unexpected result: %+v", res)
	}

	// Matching SHA-512
	if _, err := ReceiveImageWithOptions(context.Background(), root, "", "ok-sha512.iso", ImageKindISO, bytes.NewReader(payload), int64(len(payload)), ReceiveOptions{
		ChecksumAlgo:  "sha512",
		ChecksumValue: hex.EncodeToString(sha512Sum[:]),
	}); err != nil {
		t.Fatalf("sha512 failed: %v", err)
	}

	// Matching MD5
	if _, err := ReceiveImageWithOptions(context.Background(), root, "", "ok-md5.iso", ImageKindISO, bytes.NewReader(payload), int64(len(payload)), ReceiveOptions{
		ChecksumAlgo:  "md5",
		ChecksumValue: hex.EncodeToString(md5Sum[:]),
	}); err != nil {
		t.Fatalf("md5 failed: %v", err)
	}

	// Mismatched SHA-256: must return ErrChecksumMismatch and leave no file or .part
	badHash := strings.Repeat("0", 64)
	if _, err := ReceiveImageWithOptions(context.Background(), root, "", "bad.iso", ImageKindISO, bytes.NewReader(payload), -1, ReceiveOptions{
		ChecksumAlgo:  "sha256",
		ChecksumValue: badHash,
	}); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("expected ErrChecksumMismatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bad.iso")); !os.IsNotExist(err) {
		t.Fatalf("bad.iso should not exist after mismatch")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".omb-upload-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestHTTPUploadStreamAndChecksumMismatch(t *testing.T) {
	root := realTempDir(t)
	repo := &fakeUploadRepo{
		storage: Storage{ID: 1, Name: "Local", Kind: "local", Root: root, State: "ready"},
	}
	srv := NewServer(repo)
	srv.writable = func(string) bool { return true }
	h := srv.Handler()

	payload := []byte("streaming-http-iso-content")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	// Streaming chunked request (ContentLength = -1) with valid checksum
	req := httptest.NewRequest(http.MethodPost, "/storages/1/upload?name=chunked.iso&expected_kind=iso&stream=1&checksum_algo=sha256&checksum_value="+hexSum, bytes.NewReader(payload))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Streaming request with mismatched checksum -> 422
	badReq := httptest.NewRequest(http.MethodPost, "/storages/1/upload?name=bad.iso&expected_kind=iso&stream=1&checksum_algo=sha256&checksum_value="+strings.Repeat("f", 64), bytes.NewReader(payload))
	badReq.ContentLength = -1
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", badRec.Code, badRec.Body.String())
	}

	// Without stream=1, unknown ContentLength must still be 400
	noStream := httptest.NewRequest(http.MethodPost, "/storages/1/upload?name=nostream.iso&expected_kind=iso", bytes.NewReader(payload))
	noStream.ContentLength = -1
	noStreamRec := httptest.NewRecorder()
	h.ServeHTTP(noStreamRec, noStream)
	if noStreamRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without stream=1, got %d", noStreamRec.Code)
	}
}

type fakeUploadRepo struct {
	APIRepository
	storage  Storage
	recorded []Image
}

func (f *fakeUploadRepo) Storage(_ context.Context, id int64) (Storage, error) {
	if id == f.storage.ID {
		return f.storage, nil
	}
	return Storage{}, errors.New("not found")
}

func (f *fakeUploadRepo) RecordUpload(_ context.Context, img Image) error {
	f.recorded = append(f.recorded, img)
	return nil
}
