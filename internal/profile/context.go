package profile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ContextReport struct {
	Path            string   `json:"path"`
	SizeBytes       int64    `json:"sizeBytes"`
	Files           int      `json:"files"`
	Containerignore bool     `json:"containerignore"`
	SuspiciousPaths []string `json:"suspiciousPaths,omitempty"`
}

func InspectContext(root string) (ContextReport, error) {
	report := ContextReport{Path: root}
	ignoreData, err := os.ReadFile(filepath.Join(root, ".containerignore"))
	report.Containerignore = err == nil
	patterns := parseContextIgnore(ignoreData)
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return relErr
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Mode().IsRegular() {
			report.Files++
			report.SizeBytes += info.Size()
		}
		if suspiciousContextPath(relative) && !contextIgnored(filepath.ToSlash(relative), patterns) {
			report.SuspiciousPaths = append(report.SuspiciousPaths, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return ContextReport{}, fmt.Errorf("inspect build context %q: %w", root, err)
	}
	sort.Strings(report.SuspiciousPaths)
	return report, nil
}

func parseContextIgnore(data []byte) []string {
	var result []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			result = append(result, line)
		}
	}
	return result
}

func contextIgnored(relative string, patterns []string) bool {
	ignored := false
	for _, raw := range patterns {
		negated := strings.HasPrefix(raw, "!")
		pattern := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(raw, "!"), "/"), "/")
		matched, _ := filepath.Match(pattern, relative)
		if !matched {
			matched, _ = filepath.Match(pattern, filepath.Base(relative))
		}
		if !matched {
			matched = relative == pattern || strings.HasPrefix(relative, pattern+"/")
		}
		if matched {
			ignored = !negated
		}
	}
	return ignored
}

func suspiciousContextPath(relative string) bool {
	base := strings.ToLower(filepath.Base(relative))
	if base == ".env" || base == "id_rsa" || base == "id_ed25519" || base == "credentials" || base == "credentials.json" || base == "config.json" {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".kubeconfig"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}
