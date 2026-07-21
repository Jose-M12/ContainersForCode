//go:build !windows

package app

import "os"

func currentUID() int { return os.Getuid() }
func currentGID() int { return os.Getgid() }
