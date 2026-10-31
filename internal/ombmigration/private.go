package ombmigration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func jsonBytes(v any) ([]byte, error) { return json.Marshal(v) }
func privateRead(p string) ([]byte, error) {
	i, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("unsafe private file: %s", p)
	}
	if stat, ok := i.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("private file owner differs: %s", p)
	}
	return os.ReadFile(p)
}
func atomicJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(p, append(b, '\n'), 0600)
}
func atomicWrite(p string, b []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".omb-migrate-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(p))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

// MergeState locks and merges the shared system/data state atomically.
func MergeState(p string, updates map[string]any) error {
	lock, e := openPrivateLock(p + ".lock")
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	b, e := privateRead(p)
	if e != nil {
		return e
	}
	var state map[string]any
	if e = decodeJSON(b, &state); e != nil {
		return fmt.Errorf("invalid shared migration state")
	}
	for k, v := range updates {
		state[k] = v
	}
	return atomicJSON(p, state)
}

func openPrivateLock(p string) (*os.File, error) {
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	i, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || ok && s.Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, fmt.Errorf("unsafe private lock")
	}
	return f, nil
}
