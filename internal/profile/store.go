package profile

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	assets "containersagents.dev/v2"
	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/hashspec"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/state"
)

const builtinRoot = "profiles/builtin"

type Resolved struct {
	Manifest       manifest.Profile `json:"manifest"`
	Source         string           `json:"source"`
	SourcePath     string           `json:"sourcePath"`
	BaseDir        string           `json:"-"`
	EmbeddedBase   string           `json:"-"`
	Hash           string           `json:"hash"`
	ContextHash    string           `json:"contextHash,omitempty"`
	ImageReference string           `json:"imageReference"`
}

type Summary struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Mode   string `json:"mode"`
	Valid  bool   `json:"valid"`
	Error  string `json:"error,omitempty"`
}

type Store struct {
	Paths state.Paths
}

func (s Store) List() ([]Summary, error) {
	entries := map[string]Summary{}
	builtins, err := fs.ReadDir(assets.Files, builtinRoot)
	if err != nil {
		return nil, fmt.Errorf("read embedded profiles: %w", err)
	}
	for _, entry := range builtins {
		if entry.IsDir() {
			entries[entry.Name()] = Summary{Name: entry.Name(), Source: "builtin"}
		}
	}
	customEntries, err := os.ReadDir(s.Paths.ProfilesDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read custom profiles: %w", err)
	}
	for _, entry := range customEntries {
		if !entry.IsDir() {
			continue
		}
		if existing, duplicate := entries[entry.Name()]; duplicate {
			existing.Valid = false
			existing.Error = "custom profile collides with a built-in profile"
			entries[entry.Name()] = existing
			continue
		}
		entries[entry.Name()] = Summary{Name: entry.Name(), Source: "custom"}
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]Summary, 0, len(names))
	for _, name := range names {
		summary := entries[name]
		if summary.Error == "" {
			resolved, resolveErr := s.Resolve(name)
			if resolveErr != nil {
				summary.Error = resolveErr.Error()
			} else {
				summary.Valid = true
				summary.Mode = resolved.Manifest.Spec.Image.Mode
			}
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s Store) Resolve(name string) (Resolved, error) {
	if err := manifest.ValidateName(name); err != nil {
		return Resolved{}, fmt.Errorf("invalid profile name %q: %w", name, err)
	}
	customPath := filepath.Join(s.Paths.ProfilesDir(), name, "profile.json")
	_, customErr := os.Stat(customPath)
	customExists := customErr == nil
	builtinPath := builtinRoot + "/" + name + "/profile.json"
	builtinData, builtinErr := fs.ReadFile(assets.Files, builtinPath)
	builtinExists := builtinErr == nil
	if customExists && builtinExists {
		return Resolved{}, fmt.Errorf("custom profile %q collides with a built-in profile; built-ins cannot be shadowed", name)
	}
	var resolved Resolved
	if customExists {
		loaded, err := manifest.LoadProfile(customPath)
		if err != nil {
			return Resolved{}, err
		}
		resolved = Resolved{Manifest: loaded, Source: "custom", SourcePath: customPath, BaseDir: filepath.Dir(customPath)}
	} else if builtinExists {
		loaded, err := manifest.DecodeStrict[manifest.Profile](builtinData)
		if err != nil {
			return Resolved{}, fmt.Errorf("embedded profile %q: %w", name, err)
		}
		manifest.ApplyProfileDefaults(&loaded)
		if err := manifest.ValidateProfile(loaded); err != nil {
			return Resolved{}, fmt.Errorf("embedded profile %q: %w", name, err)
		}
		resolved = Resolved{Manifest: loaded, Source: "builtin", SourcePath: "embedded:" + builtinPath, EmbeddedBase: builtinRoot + "/" + name}
	} else {
		if customErr != nil && !os.IsNotExist(customErr) {
			return Resolved{}, fmt.Errorf("inspect custom profile %q: %w", name, customErr)
		}
		return Resolved{}, fmt.Errorf("profile %q does not exist", name)
	}
	if resolved.Manifest.Metadata.Name != name {
		return Resolved{}, fmt.Errorf("profile directory name %q does not match metadata.name %q", name, resolved.Manifest.Metadata.Name)
	}
	manifestHash, err := hashspec.Value(resolved.Manifest)
	if err != nil {
		return Resolved{}, err
	}
	if resolved.Manifest.Spec.Image.Mode == "build" {
		if resolved.Source == "builtin" {
			resolved.ContextHash, err = hashspec.EmbeddedDirectory(assets.Files, resolved.EmbeddedBase)
		} else {
			contextPath, pathErr := resolveProfilePath(resolved.BaseDir, resolved.Manifest.Spec.Image.Context)
			if pathErr != nil {
				return Resolved{}, fmt.Errorf("profile %q build context: %w", name, pathErr)
			}
			resolved.ContextHash, err = hashspec.Directory(contextPath)
		}
		if err != nil {
			return Resolved{}, fmt.Errorf("hash profile %q context: %w", name, err)
		}
		resolved.Hash, err = hashspec.Value(map[string]string{"manifest": manifestHash, "context": resolved.ContextHash})
	} else {
		resolved.Hash = manifestHash
	}
	if err != nil {
		return Resolved{}, err
	}
	resolved.ImageReference, err = imageReference(resolved.Manifest.Spec.Image, resolved.Hash)
	if err != nil {
		return Resolved{}, fmt.Errorf("profile %q: %w", name, err)
	}
	return resolved, nil
}

func (s Store) Materialize(resolved Resolved) (contextPath, containerfilePath string, err error) {
	if resolved.Manifest.Spec.Image.Mode != "build" {
		return "", "", fmt.Errorf("profile %q does not use build mode", resolved.Manifest.Metadata.Name)
	}
	if resolved.Source == "custom" {
		contextPath, err = resolveProfilePath(resolved.BaseDir, resolved.Manifest.Spec.Image.Context)
		if err != nil {
			return "", "", err
		}
		containerfilePath, err = resolveProfilePath(resolved.BaseDir, resolved.Manifest.Spec.Image.Containerfile)
		if err != nil {
			return "", "", err
		}
		if !fsutil.IsWithin(containerfilePath, contextPath) {
			return "", "", fmt.Errorf("Containerfile must be inside the explicit build context")
		}
		return contextPath, containerfilePath, nil
	}
	destination := s.Paths.ProfileBuildCache(resolved.Hash)
	marker := filepath.Join(destination, ".complete")
	if _, statErr := os.Stat(marker); statErr == nil {
		contextPath = filepath.Join(destination, filepath.FromSlash(resolved.Manifest.Spec.Image.Context))
		containerfilePath = filepath.Join(destination, filepath.FromSlash(resolved.Manifest.Spec.Image.Containerfile))
		return contextPath, containerfilePath, nil
	}
	if err := fsutil.EnsureDir(destination, 0700); err != nil {
		return "", "", err
	}
	err = fs.WalkDir(assets.Files, resolved.EmbeddedBase, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(current, resolved.EmbeddedBase), "/")
		if relative == "" {
			return nil
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if !fsutil.IsWithin(target, destination) {
			return fmt.Errorf("embedded profile path escaped extraction root")
		}
		if entry.IsDir() {
			return fsutil.EnsureDir(target, 0700)
		}
		data, readErr := fs.ReadFile(assets.Files, current)
		if readErr != nil {
			return readErr
		}
		return fsutil.AtomicWrite(target, data, 0600)
	})
	if err != nil {
		return "", "", fmt.Errorf("materialize profile %q: %w", resolved.Manifest.Metadata.Name, err)
	}
	if err := fsutil.AtomicWrite(marker, []byte(resolved.Hash+"\n"), 0600); err != nil {
		return "", "", err
	}
	contextPath = filepath.Join(destination, filepath.FromSlash(resolved.Manifest.Spec.Image.Context))
	containerfilePath = filepath.Join(destination, filepath.FromSlash(resolved.Manifest.Spec.Image.Containerfile))
	return contextPath, containerfilePath, nil
}

func (s Store) WriteCustom(profile manifest.Profile, files map[string][]byte) (string, error) {
	if err := manifest.ValidateProfile(profile); err != nil {
		return "", err
	}
	dir := filepath.Join(s.Paths.ProfilesDir(), profile.Metadata.Name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("profile %q already exists", profile.Metadata.Name)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if _, err := fs.Stat(assets.Files, builtinRoot+"/"+profile.Metadata.Name); err == nil {
		return "", fmt.Errorf("profile %q collides with a built-in profile", profile.Metadata.Name)
	}
	if err := fsutil.EnsureDir(dir, 0700); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "", err
	}
	if err := fsutil.AtomicWrite(filepath.Join(dir, "profile.json"), append(data, '\n'), 0600); err != nil {
		return "", err
	}
	for relative, data := range files {
		if filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
			return "", fmt.Errorf("invalid custom profile file path %q", relative)
		}
		target := filepath.Join(dir, relative)
		if !fsutil.IsWithin(target, dir) {
			return "", fmt.Errorf("custom profile file path escapes profile directory")
		}
		if err := fsutil.AtomicWrite(target, data, 0600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func secureRelative(base, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("path must be relative")
	}
	joined := filepath.Join(base, relative)
	resolved, err := fsutil.ResolveExisting(joined)
	if err != nil {
		return "", err
	}
	baseResolved, err := fsutil.ResolveExisting(base)
	if err != nil {
		return "", err
	}
	if !fsutil.IsWithin(resolved, baseResolved) {
		return "", fmt.Errorf("path %q escapes profile directory", relative)
	}
	return resolved, nil
}

func resolveProfilePath(base, declared string) (string, error) {
	if filepath.IsAbs(declared) {
		return fsutil.ResolveExisting(declared)
	}
	return secureRelative(base, declared)
}

func imageReference(image manifest.ImageSpec, hash string) (string, error) {
	if image.Mode != "build" {
		return image.Reference, nil
	}
	repository := strings.TrimSuffix(image.Repository, ":")
	lastSlash := strings.LastIndex(repository, "/")
	if strings.Contains(repository, "@") || strings.LastIndex(repository, ":") > lastSlash {
		return "", fmt.Errorf("build repository must not contain a tag or digest; V2 adds a content tag")
	}
	return repository + ":" + hash[:12], nil
}
