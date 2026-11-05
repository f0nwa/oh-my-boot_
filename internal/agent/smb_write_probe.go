package agent

import (
	"fmt"
	"io"
	"os"
	"time"
)

func probeSMBWrite(path string) error {
	done := make(chan error, 1)
	go func() {
		file, err := os.CreateTemp(path, ".omb-write-check-*")
		if err != nil {
			done <- err
			return
		}
		name := file.Name()
		defer os.Remove(name)
		payload := []byte("omb-write-check")
		if _, err := file.Write(payload); err != nil {
			_ = file.Close()
			done <- err
			return
		}
		if err := file.Close(); err != nil {
			done <- err
			return
		}
		file, err = os.Open(name)
		if err != nil {
			done <- err
			return
		}
		actual := make([]byte, len(payload))
		_, err = io.ReadFull(file, actual)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil && string(actual) != string(payload) {
			err = fmt.Errorf("write probe content differs")
		}
		if err != nil {
			done <- err
			return
		}
		done <- os.Remove(name)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(probeTimeout):
		return fmt.Errorf("no answer in %s", probeTimeout)
	}
}
