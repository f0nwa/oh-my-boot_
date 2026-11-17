package images

import (
	"sync"
	"syscall"
	"time"
)

// accessWrite is W_OK for access(2).
const accessWrite = 0x2

// spaceTimeout bounds statfs on a storage: a hung SMB mount must not hold up
// the catalog.
const spaceTimeout = 3 * time.Second

// Space reports the size of the file system holding root and the space an
// unprivileged writer can still use.
func Space(root string) (total, free int64, ok bool) {
	type result struct {
		total, free int64
		ok          bool
	}
	done := make(chan result, 1)
	go func() {
		var st syscall.Statfs_t
		if err := syscall.Statfs(root, &st); err != nil {
			done <- result{}
			return
		}
		done <- result{int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize), true}
	}()
	select {
	case r := <-done:
		return r.total, r.free, r.ok
	case <-time.After(spaceTimeout):
		return 0, 0, false
	}
}

// Writable reports whether this service may create files in dir: the
// directory permissions allow it and the file system is not read-only, also
// when systemd's ProtectSystem made it so.
func Writable(dir string) bool {
	return syscall.Access(dir, accessWrite) == nil
}

// describeSpace fills the space and upload fields of ready storages, all at
// once so that one slow storage costs one timeout.
func (s *Server) describeSpace(storages []Storage) {
	var wg sync.WaitGroup
	for i := range storages {
		storage := &storages[i]
		if storage.State != "ready" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.space != nil {
				storage.Total, storage.Free, _ = s.space(storage.Root)
			}
			switch storage.Kind {
			case "smb":
				storage.Uploads = true
			case "nfs":
				storage.Uploads = storage.UploadsEnabled
			case "local":
				storage.Uploads = s.writable != nil && validateRootPath(storage.Root) == nil && s.writable(storage.Root)
			}
		}()
	}
	wg.Wait()
}
