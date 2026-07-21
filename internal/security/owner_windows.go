//go:build windows

package security

import (
	"fmt"
	"io/fs"
)

func fileOwner(info fs.FileInfo) (int, error) {
	return 0, fmt.Errorf("numeric file ownership unavailable on Windows")
}
