package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	MaxArchiveBytes  = 1 << 30
	maxExpandedBytes = (2 << 30) + (8 << 20) // Includes tar headers and padding.
	maxPackageBytes  = 2 << 30
	maxArchiveFiles  = 4096
)

var (
	errArchiveSize = errors.New("update archive exceeds its limit")
	debFilename    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+~%:_-]{0,245}\.deb$`)
)

func extractUpdate(source io.Reader, repo string, expandedLimit int64) error {
	gz, err := gzip.NewReader(source)
	if err != nil {
		return err
	}
	defer gz.Close()
	expanded := &io.LimitedReader{R: gz, N: expandedLimit + 1}
	stream := tar.NewReader(expanded)
	var files int
	var payload int64
	for {
		entry, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name, ok := strings.CutPrefix(entry.Name, "apt-repo/")
		if !ok || entry.Typeflag != tar.TypeReg || len(entry.PAXRecords) != 0 {
			return fmt.Errorf("unsupported update archive entry %q", entry.Name)
		}
		switch name {
		case "repo-manifest.json":
			if entry.Size > 4<<20 {
				return errArchiveSize
			}
		case "repo-manifest.sig":
			if entry.Size > 64<<10 {
				return errArchiveSize
			}
		case "Packages.gz":
			if entry.Size > 16<<20 {
				return errArchiveSize
			}
		default:
			if !debFilename.MatchString(name) {
				return fmt.Errorf("unexpected repository filename %q", name)
			}
		}
		files++
		if files > maxArchiveFiles || entry.Size > maxPackageBytes-payload || entry.Size >= expanded.N {
			return errArchiveSize
		}
		payload += entry.Size
		file, err := os.OpenFile(filepath.Join(repo, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, stream)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
	}
	// Drain tar padding and verify the gzip trailer within the same limit.
	if _, err := io.Copy(io.Discard, expanded); err != nil {
		return err
	}
	if expanded.N == 0 {
		return errArchiveSize
	}
	if files == 0 {
		return errors.New("empty update archive")
	}
	return nil
}

func (s *Service) stageArchive(ctx context.Context, source io.Reader) (Release, int, string) {
	stage := filepath.Join(s.dir, "stage")
	if err := os.RemoveAll(stage); err != nil {
		log.Printf("remove previous update stage: %v", err)
		return Release{}, 500, "service_error"
	}
	dir, err := os.MkdirTemp(s.dir, "upload-")
	if err != nil {
		log.Printf("make update stage: %v", err)
		return Release{}, 500, "service_error"
	}
	defer os.RemoveAll(dir)
	repo := filepath.Join(dir, "apt-repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		log.Printf("make update repository: %v", err)
		return Release{}, 500, "service_error"
	}
	if err := extractUpdate(source, repo, maxExpandedBytes); err != nil {
		log.Printf("extract update archive: %v", err)
		code, reason := archiveFailure(err)
		return Release{}, code, reason
	}
	release, err := s.installer.Check(ctx, repo)
	if err != nil {
		log.Printf("preflight OMB update: %v", err)
		return Release{}, 422, "verification_failed"
	}
	if err := os.Rename(dir, stage); err != nil {
		log.Printf("publish update stage: %v", err)
		return Release{}, 500, "service_error"
	}
	return release, 200, ""
}
