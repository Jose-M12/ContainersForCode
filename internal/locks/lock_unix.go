//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package locks

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"containersagents.dev/v2/internal/fsutil"
)

type fileLock struct {
	file *os.File
}

func Acquire(path string) (Lock, error) {
	if err := fsutil.EnsureDir(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open lock %q: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, &BusyError{Path: path}
		}
		return nil, fmt.Errorf("acquire lock %q: %w", path, err)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
		_ = file.Sync()
	}
	return &fileLock{file: file}, nil
}

func (l *fileLock) Release() error {
	if l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
