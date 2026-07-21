package hashspec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

func Value(value any) (string, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("serialize canonical value: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func Directory(root string) (string, error) {
	hasher := sha256.New()
	var paths []string
	ignore, err := loadIgnoreFile(root)
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if relative != "." && !entry.IsDir() && ignore.ignored(filepath.ToSlash(relative)) {
			return nil
		}
		if relative != "." {
			paths = append(paths, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk build context %q: %w", root, err)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(fullPath)
		if err != nil {
			return "", fmt.Errorf("inspect %q: %w", fullPath, err)
		}
		fmt.Fprintf(hasher, "%s\x00%d\x00", relative, info.Mode())
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(fullPath)
			if err != nil {
				return "", fmt.Errorf("resolve context symlink %q: %w", fullPath, err)
			}
			fmt.Fprint(hasher, target)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(fullPath)
		if err != nil {
			return "", fmt.Errorf("open %q: %w", fullPath, err)
		}
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash %q: %w", fullPath, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close %q: %w", fullPath, closeErr)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

type ignorePatterns []string

func loadIgnoreFile(root string) (ignorePatterns, error) {
	data, err := os.ReadFile(filepath.Join(root, ".containerignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read .containerignore: %w", err)
	}
	var patterns ignorePatterns
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

func (patterns ignorePatterns) ignored(relative string) bool {
	relative = path.Clean(strings.TrimPrefix(relative, "./"))
	if relative == ".containerignore" || strings.EqualFold(path.Base(relative), "Containerfile") {
		return false
	}
	ignored := false
	for _, raw := range patterns {
		negated := strings.HasPrefix(raw, "!")
		pattern := strings.TrimPrefix(raw, "!")
		pattern = strings.TrimPrefix(pattern, "/")
		pattern = strings.TrimSuffix(pattern, "/")
		if pattern == "" {
			continue
		}
		matched := false
		if strings.Contains(pattern, "/") {
			matched, _ = path.Match(pattern, relative)
		} else {
			matched, _ = path.Match(pattern, path.Base(relative))
			if !matched {
				matched = relative == pattern || strings.HasPrefix(relative, pattern+"/")
			}
		}
		if matched {
			ignored = !negated
		}
	}
	return ignored
}

func EmbeddedDirectory(source fs.FS, root string) (string, error) {
	hasher := sha256.New()
	var paths []string
	err := fs.WalkDir(source, root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current != root {
			paths = append(paths, current)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk embedded build context %q: %w", root, err)
	}
	sort.Strings(paths)
	for _, current := range paths {
		entry, err := fs.Stat(source, current)
		if err != nil {
			return "", err
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(current, root), "/")
		fmt.Fprintf(hasher, "%s\x00%d\x00", path.Clean(relative), entry.Mode())
		if entry.Mode().IsRegular() {
			file, err := source.Open(current)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(hasher, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
