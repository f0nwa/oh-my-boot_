package ueficausb

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeRunner imitates wimlib-imagex: export writes the target WIM, extract
// creates the 2023 loader and one font, update records its commands.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []string
	inputs  []string
	fail    string
	noFonts bool
}

func (f *fakeRunner) Run(_ context.Context, input, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	f.inputs = append(f.inputs, input)
	if f.fail != "" && strings.Contains(call, f.fail) {
		return nil, errors.New("simulated wimlib failure")
	}
	switch args[0] {
	case "export":
		return nil, os.WriteFile(args[3], []byte("exported-wim"), 0644)
	case "extract":
		dest := ""
		for _, a := range args {
			if strings.HasPrefix(a, "--dest-dir=") {
				dest = strings.TrimPrefix(a, "--dest-dir=")
			}
		}
		if err := os.MkdirAll(dest, 0755); err != nil {
			return nil, err
		}
		if strings.Contains(call, "bootmgfw_EX.efi") {
			return nil, os.WriteFile(filepath.Join(dest, "bootmgfw_EX.efi"), []byte("ex-loader"), 0644)
		}
		if strings.Contains(call, "Fonts_EX") && !f.noFonts {
			return nil, os.WriteFile(filepath.Join(dest, "segmono_boot_EX.ttf"), []byte("ex-font"), 0644)
		}
	}
	return nil, nil
}

func (f *fakeRunner) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func writeFile(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

// newBuilder returns a builder over a fake ISO of profile 17.
func newBuilder(t *testing.T) (*Builder, *fakeRunner) {
	t.Helper()
	media := t.TempDir()
	iso := filepath.Join(media, "17")
	writeFile(t, filepath.Join(iso, "bootmgr"), "bios-loader")
	writeFile(t, filepath.Join(iso, "bootmgr.efi"), "pca2011-bootmgr")
	writeFile(t, filepath.Join(iso, "boot", "bcd"), "bios-bcd")
	writeFile(t, filepath.Join(iso, "boot", "boot.sdi"), "sdi")
	writeFile(t, filepath.Join(iso, "efi", "boot", "bootx64.efi"), "pca2011-loader")
	writeFile(t, filepath.Join(iso, "efi", "microsoft", "boot", "bcd"), "efi-bcd")
	writeFile(t, filepath.Join(iso, "efi", "microsoft", "boot", "fonts", "segmono_boot.ttf"), "old-font")
	writeFile(t, filepath.Join(iso, "sources", "boot.wim"), "source-wim-1")
	writeFile(t, filepath.Join(iso, "sources", "install.wim"), "not on the stick")
	runner := &fakeRunner{}
	return &Builder{MediaRoot: media, CacheRoot: t.TempDir(), Runner: runner}, runner
}

func readZip(t *testing.T, b *Builder, r Result) map[string]*zip.File {
	t.Helper()
	f, size, err := b.Open(r.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if size != r.Size {
		t.Fatalf("size %d, result %d", size, r.Size)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), size)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*zip.File{}
	for _, e := range z.File {
		files[e.Name] = e
	}
	return files
}

func content(t *testing.T, f *zip.File) string {
	t.Helper()
	if f == nil {
		t.Fatal("missing zip entry")
	}
	r, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func names(files map[string]*zip.File) string {
	var list []string
	for n := range files {
		list = append(list, n)
	}
	sort.Strings(list)
	return strings.Join(list, " ")
}

func TestBuildPCA2011Archive(t *testing.T) {
	b, runner := newBuilder(t)
	r, err := b.Build(context.Background(), 17, LoaderPCA2011)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.Key, "17-2011-") || len(r.SHA256) != 64 {
		t.Fatalf("result %+v", r)
	}
	files := readZip(t, b, r)
	want := "README.txt boot/bcd boot/boot.sdi bootmgr bootmgr.efi efi/boot/bootx64.efi efi/microsoft/boot/bcd efi/microsoft/boot/fonts/segmono_boot.ttf sources/boot.wim"
	if got := names(files); got != want {
		t.Fatalf("entries:\n%s\nwant:\n%s", got, want)
	}
	if content(t, files["efi/boot/bootx64.efi"]) != "pca2011-loader" || content(t, files["bootmgr.efi"]) != "pca2011-bootmgr" {
		t.Fatal("2011 loaders changed")
	}
	if content(t, files["sources/boot.wim"]) != "exported-wim" || files["sources/boot.wim"].Method != zip.Store {
		t.Fatal("boot.wim is not the stored export")
	}
	if content(t, files["README.txt"]) != string(ReadMe()) {
		t.Fatal("README differs")
	}
	export := runner.calls[0]
	if !strings.HasPrefix(export, "wimlib-imagex export ") || !strings.Contains(export, filepath.Join("17", "sources", "boot.wim")+" 1 ") || !strings.HasSuffix(export, " --boot") {
		t.Fatalf("export call %q", export)
	}
	update := -1
	for i, c := range runner.calls {
		if strings.HasPrefix(c, "wimlib-imagex update ") {
			update = i
		}
	}
	if update < 0 || !strings.HasSuffix(runner.calls[update], " 1") {
		t.Fatalf("calls %v", runner.calls)
	}
	lines := strings.Split(strings.TrimSpace(runner.inputs[update]), "\n")
	if len(lines) != 3 {
		t.Fatalf("update input %q", runner.inputs[update])
	}
	for i, name := range []string{"winpeshl.ini", "uefica.cmd", "uefica.vbs"} {
		if !strings.HasPrefix(lines[i], "add '") || !strings.HasSuffix(lines[i], "' '/Windows/System32/"+name+"'") {
			t.Fatalf("update line %q", lines[i])
		}
	}
	if runner.count("wimlib-imagex extract") != 0 {
		t.Fatal("2011 stick must not extract the 2023 loader")
	}
}

func TestBuild2023ReplacesLoaders(t *testing.T) {
	b, _ := newBuilder(t)
	r, err := b.Build(context.Background(), 17, LoaderUEFICA2023)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Key, "-2023-") {
		t.Fatalf("key %q", r.Key)
	}
	files := readZip(t, b, r)
	if content(t, files["efi/boot/bootx64.efi"]) != "ex-loader" || content(t, files["bootmgr.efi"]) != "ex-loader" {
		t.Fatal("2023 loader not in place")
	}
	if content(t, files["efi/microsoft/boot/fonts/segmono_boot.ttf"]) != "ex-font" {
		t.Fatal("Fonts_EX font not in place")
	}
	if content(t, files["boot/bcd"]) != "bios-bcd" || content(t, files["bootmgr"]) != "bios-loader" {
		t.Fatal("boot/ or bootmgr changed")
	}
	if _, ok := files["efi/microsoft/boot/fonts/segmono_boot_EX.ttf"]; ok {
		t.Fatal("_EX name kept")
	}
}

func TestBuild2023WithoutFontsEX(t *testing.T) {
	b, runner := newBuilder(t)
	runner.noFonts = true
	r, err := b.Build(context.Background(), 17, LoaderUEFICA2023)
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, b, r)
	if content(t, files["efi/microsoft/boot/fonts/segmono_boot.ttf"]) != "old-font" {
		t.Fatal("ISO font lost")
	}
}

func TestBuildUsesCache(t *testing.T) {
	b, runner := newBuilder(t)
	first, err := b.Build(context.Background(), 17, LoaderPCA2011)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(runner.calls)
	second, err := b.Build(context.Background(), 17, LoaderPCA2011)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(runner.calls) != calls {
		t.Fatalf("cache missed: %+v %+v, calls %d -> %d", first, second, calls, len(runner.calls))
	}
}

func TestBuildRebuildsWhenBootWIMChanges(t *testing.T) {
	b, _ := newBuilder(t)
	first, err := b.Build(context.Background(), 17, LoaderPCA2011)
	if err != nil {
		t.Fatal(err)
	}
	other, err := b.Build(context.Background(), 17, LoaderUEFICA2023)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(b.MediaRoot, "17", "sources", "boot.wim"), "source-wim-2")
	second, err := b.Build(context.Background(), 17, LoaderPCA2011)
	if err != nil {
		t.Fatal(err)
	}
	if second.Key == first.Key {
		t.Fatal("same key for another boot.wim")
	}
	if _, err := os.Stat(filepath.Join(b.CacheRoot, first.Key+".zip")); !os.IsNotExist(err) {
		t.Fatal("old archive kept")
	}
	if _, err := os.Stat(filepath.Join(b.CacheRoot, other.Key+".zip")); err != nil {
		t.Fatal("the 2023 archive was removed by a 2011 build")
	}
}

func TestBuildConcurrentSameKey(t *testing.T) {
	b, runner := newBuilder(t)
	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = b.Build(context.Background(), 17, LoaderPCA2011)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0] != results[1] {
		t.Fatalf("%v %v %+v", errs[0], errs[1], results)
	}
	if runner.count("wimlib-imagex export") != 1 {
		t.Fatalf("export ran %d times", runner.count("wimlib-imagex export"))
	}
	readZip(t, b, results[0])
}

func TestBuildRejects(t *testing.T) {
	b, runner := newBuilder(t)
	if _, err := b.Build(context.Background(), 0, LoaderPCA2011); !errors.Is(err, ErrInvalid) {
		t.Fatalf("id 0: %v", err)
	}
	if _, err := b.Build(context.Background(), 17, Loader("2024")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("loader: %v", err)
	}
	if _, err := b.Build(context.Background(), 99, LoaderPCA2011); !errors.Is(err, ErrMissing) {
		t.Fatalf("no profile: %v", err)
	}
	link := filepath.Join(b.MediaRoot, "17", "efi", "evil")
	if err := os.Symlink("/etc/passwd", link); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(context.Background(), 17, LoaderPCA2011); !errors.Is(err, ErrMissing) {
		t.Fatalf("symlink: %v", err)
	}
	os.Remove(link)
	os.Remove(filepath.Join(b.MediaRoot, "17", "bootmgr.efi"))
	if _, err := b.Build(context.Background(), 17, LoaderPCA2011); !errors.Is(err, ErrMissing) {
		t.Fatalf("no bootmgr.efi: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner called: %v", runner.calls)
	}
}

func TestBuildFailureLeavesNothing(t *testing.T) {
	b, runner := newBuilder(t)
	runner.fail = "update"
	if _, err := b.Build(context.Background(), 17, LoaderPCA2011); err == nil {
		t.Fatal("no error")
	}
	entries, err := os.ReadDir(b.CacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("left in cache: %v", entries)
	}
}

func TestBuildChecksFreeSpace(t *testing.T) {
	b, runner := newBuilder(t)
	b.FreeBytes = func(string) (uint64, error) { return 1, nil }
	if _, err := b.Build(context.Background(), 17, LoaderPCA2011); err == nil || !strings.Contains(err.Error(), "not enough space") {
		t.Fatalf("err %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatal("runner called")
	}
}

func TestOpenRejectsBadKeys(t *testing.T) {
	b, _ := newBuilder(t)
	for _, key := range []string{"../x", "17-2011-zz", "0-2011-0123456789abcdef", "17-2011-0123456789abcdef/../../x"} {
		if _, _, err := b.Open(key); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", key, err)
		}
	}
	if _, _, err := b.Open("17-2011-0123456789abcdef"); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing: %v", err)
	}
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, input, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %v: %w: %s", name, args, err, out)
	}
	return out, nil
}

// TestBuildWithRealWimlib checks the wimlib-imagex command lines on a real
// two-image WIM whose boot image is 2 (like boot.wim of a Windows ISO).
func TestBuildWithRealWimlib(t *testing.T) {
	if _, err := exec.LookPath("wimlib-imagex"); err != nil || testing.Short() {
		t.Skip("wimlib-imagex is not installed")
	}
	b, _ := newBuilder(t)
	b.Runner = execRunner{}
	tree := t.TempDir()
	writeFile(t, filepath.Join(tree, "Windows", "System32", "cscript.exe"), "cscript")
	writeFile(t, filepath.Join(tree, "Windows", "Boot", "EFI_EX", "bootmgfw_EX.efi"), "real-ex-loader")
	writeFile(t, filepath.Join(tree, "Windows", "Boot", "Fonts_EX", "wgl4_boot_EX.ttf"), "real-ex-font")
	wim := filepath.Join(b.MediaRoot, "17", "sources", "boot.wim")
	os.Remove(wim)
	for _, args := range [][]string{
		{"capture", tree, wim, "WinPE"},
		{"append", tree, wim, "Setup"},
		{"info", wim, "2", "--boot"},
	} {
		if out, err := exec.Command("wimlib-imagex", args...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", args, err, out)
		}
	}
	r, err := b.Build(context.Background(), 17, LoaderUEFICA2023)
	if err != nil {
		t.Fatal(err)
	}
	files := readZip(t, b, r)
	if content(t, files["efi/boot/bootx64.efi"]) != "real-ex-loader" || content(t, files["efi/microsoft/boot/fonts/wgl4_boot.ttf"]) != "real-ex-font" {
		t.Fatal("2023 loader or font missing")
	}
	stick := filepath.Join(t.TempDir(), "boot.wim")
	writeFile(t, stick, content(t, files["sources/boot.wim"]))
	info, err := exec.Command("wimlib-imagex", "info", stick).CombinedOutput()
	if err != nil || !regexp.MustCompile(`(?m)^Image Count:\s+1$`).Match(info) || !regexp.MustCompile(`(?m)^Boot Index:\s+1$`).Match(info) {
		t.Fatalf("stick boot.wim: %v\n%s", err, info)
	}
	dir, err := exec.Command("wimlib-imagex", "dir", stick, "1", "--path=/Windows/System32").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"/Windows/System32/winpeshl.ini", "/Windows/System32/uefica.cmd", "/Windows/System32/uefica.vbs", "/Windows/System32/cscript.exe"} {
		if !strings.Contains(string(dir), name) {
			t.Fatalf("%s missing:\n%s", name, dir)
		}
	}
}
