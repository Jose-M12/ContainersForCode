package security

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/state"
)

type MountValidator struct {
	Paths        state.Paths
	Home         string
	AllowedRoots []string
	UID          int
}

type MountResult struct {
	Source   string   `json:"source"`
	Warnings []string `json:"warnings,omitempty"`
}

func (v MountValidator) Validate(source string, allowDangerous bool) (MountResult, error) {
	resolved, err := fsutil.ResolveExisting(source)
	if err != nil {
		return MountResult{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return MountResult{}, err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return MountResult{}, fmt.Errorf("mount source %q must be a regular file or directory", resolved)
	}
	if strings.Contains(resolved, ",") {
		return MountResult{}, fmt.Errorf("mount source %q contains a comma delimiter unsupported by Podman --mount", resolved)
	}
	home, err := fsutil.ResolveExisting(v.Home)
	if err != nil {
		return MountResult{}, fmt.Errorf("resolve host home: %w", err)
	}
	if resolved == home {
		return MountResult{}, fmt.Errorf("full host home mounts are prohibited")
	}
	for _, denied := range hardDenied(v.Paths, v.UID) {
		if resolved == denied || (denied != string(filepath.Separator) && fsutil.IsWithin(resolved, denied)) {
			return MountResult{}, fmt.Errorf("mount source %q is inside prohibited host path %q", resolved, denied)
		}
	}
	base := strings.ToLower(filepath.Base(resolved))
	if base == "podman.sock" || base == "docker.sock" {
		return MountResult{}, fmt.Errorf("container engine socket mounts are prohibited")
	}
	roots := v.AllowedRoots
	if len(roots) == 0 {
		roots = []string{home}
	}
	allowed := false
	for _, root := range roots {
		resolvedRoot, rootErr := fsutil.ResolveExisting(root)
		if rootErr != nil {
			continue
		}
		if fsutil.IsWithin(resolved, resolvedRoot) {
			allowed = true
			break
		}
	}
	result := MountResult{Source: resolved}
	if !allowed {
		if !allowDangerous {
			return MountResult{}, fmt.Errorf("mount source %q is outside configured allowedMountRoots", resolved)
		}
		result.Warnings = append(result.Warnings, "dangerous mount override: source is outside configured allowed roots")
	}
	if runtime.GOOS != "windows" && v.UID >= 0 {
		owner, ownerErr := fileOwner(info)
		if ownerErr == nil && owner != v.UID {
			result.Warnings = append(result.Warnings, fmt.Sprintf("mount source is owned by UID %d, not current UID %d", owner, v.UID))
		}
	}
	return result, nil
}

func hardDenied(paths state.Paths, uid int) []string {
	values := []string{"/", "/boot", "/dev", "/etc", "/proc", "/root", "/run", "/sys", "/usr", "/var", paths.Config, paths.Data, paths.State, paths.Cache}
	if uid >= 0 {
		values = append(values, fmt.Sprintf("/run/user/%d", uid))
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err == nil {
			result = append(result, filepath.Clean(absolute))
		}
	}
	return result
}
