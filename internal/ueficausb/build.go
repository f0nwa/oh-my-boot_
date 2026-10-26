package ueficausb

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Runner runs a command with standard input; agent.ExecDriverCommandRunner fits.
type Runner interface {
	Run(ctx context.Context, input, name string, args ...string) ([]byte, error)
}

// Loader selects the UEFI boot loader of the stick.
type Loader string

const (
	// LoaderPCA2011 keeps the loaders of the ISO (Windows Production PCA 2011).
	LoaderPCA2011 Loader = "2011"
	// LoaderUEFICA2023 uses Windows/Boot/EFI_EX/bootmgfw_EX.efi (Windows UEFI CA 2023).
	LoaderUEFICA2023 Loader = "2023"
)

// Result names a built archive.
type Result struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var (
	// ErrMissing: the profile ISO is not mounted or lacks a file the stick needs.
	ErrMissing = errors.New("profile media is missing")
	// ErrInvalid: a bad profile ID, loader or key.
	ErrInvalid = errors.New("invalid request")
)

var keyPattern = regexp.MustCompile(`^[1-9][0-9]{0,18}-(2011|2023)-[0-9a-f]{16}$`)

// Builder makes stick archives from mounted profile ISOs (MediaRoot/<ID>)
// and keeps them in CacheRoot.
type Builder struct {
	MediaRoot string
	CacheRoot string
	Runner    Runner
	// FreeBytes reports the free space of a directory; nil skips the check.
	FreeBytes func(string) (uint64, error)

	mu sync.Mutex
}

// mediaFile is a file of the ISO copied to the stick.
type mediaFile struct {
	name   string // path in the archive, as on the ISO
	source string // file to read, empty when data is set
	data   []byte
	store  bool
}

// Build returns the archive of the stick for a profile, building it when the
// cache has none for the current boot.wim, scripts and loader.
func (b *Builder) Build(ctx context.Context, profileID int64, loader Loader) (Result, error) {
	if profileID <= 0 || (loader != LoaderPCA2011 && loader != LoaderUEFICA2023) {
		return Result{}, ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	root := filepath.Join(b.MediaRoot, strconv.FormatInt(profileID, 10))
	files, bootWIM, err := scanMedia(root)
	if err != nil {
		return Result{}, err
	}
	sourceHash, sourceSize, err := hashFile(bootWIM)
	if err != nil {
		return Result{}, ErrMissing
	}
	sum := sha256.Sum256([]byte(sourceHash + "\x00" + ScriptsVersion() + "\x00" + string(loader)))
	key := fmt.Sprintf("%d-%s-%s", profileID, loader, hex.EncodeToString(sum[:])[:16])
	final := filepath.Join(b.CacheRoot, key+".zip")
	if info, err := os.Stat(final); err == nil && info.Mode().IsRegular() {
		if digest, err := os.ReadFile(final + ".sha256"); err == nil && len(digest) == 64 {
			return Result{Key: key, Size: info.Size(), SHA256: string(digest)}, nil
		}
	}
	if err := os.MkdirAll(b.CacheRoot, 0750); err != nil {
		return Result{}, err
	}
	if b.FreeBytes != nil {
		free, err := b.FreeBytes(b.CacheRoot)
		if err != nil {
			return Result{}, err
		}
		if free < 2*uint64(sourceSize) {
			return Result{}, fmt.Errorf("not enough space in %s for the USB stick archive", b.CacheRoot)
		}
	}
	tmp, err := os.MkdirTemp(b.CacheRoot, ".build-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(tmp)

	wim := filepath.Join(tmp, "boot.wim")
	if _, err := b.Runner.Run(ctx, "", "wimlib-imagex", "export", bootWIM, "1", wim, "--boot"); err != nil {
		return Result{}, err
	}
	if err := b.addScripts(ctx, tmp, wim); err != nil {
		return Result{}, err
	}
	files = append(files, mediaFile{name: "sources/boot.wim", source: wim, store: true}, mediaFile{name: "README.txt", data: ReadMe()})
	if loader == LoaderUEFICA2023 {
		if files, err = b.use2023Loader(ctx, tmp, bootWIM, files); err != nil {
			return Result{}, err
		}
	}
	out := filepath.Join(tmp, "out.zip")
	size, digest, err := writeZip(out, files)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(out+".sha256", []byte(digest), 0640); err != nil {
		return Result{}, err
	}
	if err := os.Rename(out, final); err != nil {
		return Result{}, err
	}
	if err := os.Rename(out+".sha256", final+".sha256"); err != nil {
		os.Remove(final)
		return Result{}, err
	}
	b.removeOld(fmt.Sprintf("%d-%s-", profileID, loader), key)
	return Result{Key: key, Size: size, SHA256: digest}, nil
}

// Open opens a built archive by its key.
func (b *Builder) Open(key string) (*os.File, int64, error) {
	if !keyPattern.MatchString(key) || strings.HasPrefix(key, "0") {
		return nil, 0, ErrInvalid
	}
	f, err := os.Open(filepath.Join(b.CacheRoot, key+".zip"))
	if err != nil {
		return nil, 0, ErrMissing
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, 0, ErrMissing
	}
	return f, info.Size(), nil
}

// scanMedia lists bootmgr, bootmgr.efi, boot/ and efi/ of a mounted ISO and
// returns them with the path of sources/boot.wim. Symbolic links and missing
// required files are ErrMissing.
func scanMedia(root string) ([]mediaFile, string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, "", ErrMissing
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, "", ErrMissing
	}
	var files []mediaFile
	bootWIM := ""
	for _, e := range entries {
		lower := strings.ToLower(e.Name())
		full := filepath.Join(root, e.Name())
		switch lower {
		case "bootmgr", "bootmgr.efi":
			if !regularFile(full) {
				return nil, "", ErrMissing
			}
			files = append(files, mediaFile{name: e.Name(), source: full})
		case "boot", "efi":
			if !e.IsDir() {
				return nil, "", ErrMissing
			}
			err := filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.Type()&fs.ModeSymlink != 0 {
					return ErrMissing
				}
				if d.IsDir() {
					return nil
				}
				if !d.Type().IsRegular() {
					return ErrMissing
				}
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				files = append(files, mediaFile{name: filepath.ToSlash(rel), source: p})
				return nil
			})
			if err != nil {
				return nil, "", ErrMissing
			}
		case "sources":
			if !e.IsDir() {
				return nil, "", ErrMissing
			}
			if inner, err := os.ReadDir(full); err == nil {
				for _, s := range inner {
					if strings.EqualFold(s.Name(), "boot.wim") {
						bootWIM = filepath.Join(full, s.Name())
					}
				}
			}
		}
	}
	required := map[string]bool{"bootmgr": false, "bootmgr.efi": false, "efi/boot/bootx64.efi": false}
	for _, f := range files {
		if _, ok := required[strings.ToLower(f.name)]; ok {
			required[strings.ToLower(f.name)] = true
		}
	}
	for _, found := range required {
		if !found {
			return nil, "", ErrMissing
		}
	}
	if bootWIM == "" || !regularFile(bootWIM) {
		return nil, "", ErrMissing
	}
	return files, bootWIM, nil
}

func regularFile(name string) bool {
	info, err := os.Lstat(name)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

// addScripts puts winpeshl.ini, uefica.cmd and uefica.vbs into Windows/System32 of image 1.
func (b *Builder) addScripts(ctx context.Context, tmp, wim string) error {
	dir := filepath.Join(tmp, "files")
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	var commands strings.Builder
	for _, f := range WIMFiles() {
		name := filepath.Join(dir, f.Name)
		if err := os.WriteFile(name, f.Data, 0644); err != nil {
			return err
		}
		fmt.Fprintf(&commands, "add '%s' '/Windows/System32/%s'\n", name, f.Name)
	}
	_, err := b.Runner.Run(ctx, commands.String(), "wimlib-imagex", "update", wim, "1")
	return err
}

// use2023Loader replaces efi/boot/bootx64.efi and bootmgr.efi with
// bootmgfw_EX.efi from image 1 and the fonts with Fonts_EX (the "_EX" suffix
// removed), as Microsoft's Make2023BootableMedia.ps1 does.
func (b *Builder) use2023Loader(ctx context.Context, tmp, bootWIM string, files []mediaFile) ([]mediaFile, error) {
	ex := filepath.Join(tmp, "ex")
	if _, err := b.Runner.Run(ctx, "", "wimlib-imagex", "extract", bootWIM, "1", "/Windows/Boot/EFI_EX/bootmgfw_EX.efi", "--dest-dir="+ex, "--no-acls"); err != nil {
		return nil, err
	}
	loader := filepath.Join(ex, "bootmgfw_EX.efi")
	if !regularFile(loader) {
		return nil, fmt.Errorf("boot.wim has no Windows/Boot/EFI_EX/bootmgfw_EX.efi")
	}
	fonts := filepath.Join(tmp, "fonts")
	if _, err := b.Runner.Run(ctx, "", "wimlib-imagex", "extract", bootWIM, "1", "/Windows/Boot/Fonts_EX/*", "--dest-dir="+fonts, "--no-acls", "--nullglob"); err != nil {
		return nil, err
	}
	index := map[string]int{}
	fontDir := "efi/microsoft/boot/fonts"
	for i, f := range files {
		lower := strings.ToLower(f.name)
		index[lower] = i
		if strings.HasPrefix(lower, "efi/microsoft/boot/fonts/") {
			fontDir = path.Dir(f.name)
		}
	}
	replace := func(name, source string) {
		if i, ok := index[strings.ToLower(name)]; ok {
			files[i].source, files[i].data = source, nil
			return
		}
		index[strings.ToLower(name)] = len(files)
		files = append(files, mediaFile{name: name, source: source})
	}
	replace("efi/boot/bootx64.efi", loader)
	replace("bootmgr.efi", loader)
	if entries, err := os.ReadDir(fonts); err == nil {
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			name := e.Name()
			ext := path.Ext(name)
			base := strings.TrimSuffix(name, ext)
			if strings.HasSuffix(strings.ToLower(base), "_ex") {
				base = base[:len(base)-3]
			}
			replace(fontDir+"/"+base+ext, filepath.Join(fonts, name))
		}
	}
	return files, nil
}

// writeZip writes the files sorted by name; boot.wim is stored, the rest deflated.
func writeZip(name string, files []mediaFile) (int64, string, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	out, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
	if err != nil {
		return 0, "", err
	}
	defer out.Close()
	h := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(out, h)}
	z := zip.NewWriter(counter)
	for _, f := range files {
		method := zip.Deflate
		if f.store {
			method = zip.Store
		}
		w, err := z.CreateHeader(&zip.FileHeader{Name: f.name, Method: method})
		if err != nil {
			return 0, "", err
		}
		if f.source == "" {
			if _, err := w.Write(f.data); err != nil {
				return 0, "", err
			}
			continue
		}
		src, err := os.Open(f.source)
		if err != nil {
			return 0, "", err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return 0, "", err
		}
	}
	if err := z.Close(); err != nil {
		return 0, "", err
	}
	if err := out.Sync(); err != nil {
		return 0, "", err
	}
	return counter.n, hex.EncodeToString(h.Sum(nil)), nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// removeOld deletes earlier archives of the same profile and loader.
func (b *Builder) removeOld(prefix, keep string) {
	entries, err := os.ReadDir(b.CacheRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, prefix) && !strings.HasPrefix(name, keep+".") {
			os.Remove(filepath.Join(b.CacheRoot, name))
		}
	}
}

func hashFile(name string) (string, int64, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Archive is a built stick archive in the cache.
type Archive struct {
	ProfileID int64  `json:"profile_id"`
	Loader    Loader `json:"loader"`
	Size      int64  `json:"size"`
}

// Archives lists the finished archives of the cache by file name, without
// hashing anything. An archive of a profile re-prepared since is still
// listed: the next Build notices the new boot.wim and replaces it.
func (b *Builder) Archives() ([]Archive, error) {
	entries, err := os.ReadDir(b.CacheRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return []Archive{}, nil
	}
	if err != nil {
		return nil, err
	}
	list := []Archive{}
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".zip")
		if !ok || !keyPattern.MatchString(key) || !e.Type().IsRegular() {
			continue
		}
		if _, err := os.Stat(filepath.Join(b.CacheRoot, e.Name()+".sha256")); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		parts := strings.SplitN(key, "-", 3)
		id, _ := strconv.ParseInt(parts[0], 10, 64)
		list = append(list, Archive{ProfileID: id, Loader: Loader(parts[1]), Size: info.Size()})
	}
	return list, nil
}
