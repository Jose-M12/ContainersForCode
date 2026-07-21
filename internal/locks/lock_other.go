//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package locks

import (
	"fmt"
	"os"
	"path/filepath"

	"containersagents.dev/v2/internal/fsutil"
)

type fileLock struct {
	path string
	file *os.File
}

func Acquire(path string) (Lock, error) {
	if err := fsutil.EnsureDir(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if os.IsExist(err) {
		return nil, &BusyError{Path: path}
	}
	if err != nil {
		return nil, fmt.Errorf("create lock %q: %w", path, err)
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	return &fileLock{path: path, file: file}, nil
}

func (l *fileLock) Release() error {
	if l.file == nil {
		return nil
	}
	closeErr := l.file.Close()
	removeErr := os.Remove(l.path)
	l.file = nil
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}
