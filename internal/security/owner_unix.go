//go:build !windows

package security

import (
	"fmt"
	"io/fs"
	"syscall"
)

func fileOwner(info fs.FileInfo) (int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("file owner information unavailable")
	}
	return int(stat.Uid), nil
}
