package profile

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	assets "containersagents.dev/v2"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/state"
)

var immutableImageDigest = regexp.MustCompile(`@sha256:[0-9a-f]{64}(?:\s|$)`)

func TestAllBuiltinsResolve(t *testing.T) {
	root := t.TempDir()
	store := Store{Paths: state.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"), State: filepath.Join(root, "state"), Cache: filepath.Join(root, "cache")}}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("expected five built-ins, got %d", len(items))
	}
	for _, item := range items {
		if !item.Valid {
			t.Errorf("%s invalid: %s", item.Name, item.Error)
		}
	}
}

func TestBuiltinImagesArePinnedToImmutableDigests(t *testing.T) {
	err := fs.WalkDir(assets.Files, builtinRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		switch filepath.Base(path) {
		case "Containerfile":
			data, err := fs.ReadFile(assets.Files, path)
			if err != nil {
				return err
			}
			for lineNumber, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(strings.ToUpper(line), "FROM ") && !immutableImageDigest.MatchString(line) {
					t.Errorf("%s:%d has an unpinned base image: %s", path, lineNumber+1, line)
				}
			}
		case "profile.json":
			data, err := fs.ReadFile(assets.Files, path)
			if err != nil {
				return err
			}
			profile, err := manifest.DecodeStrict[manifest.Profile](data)
			if err != nil {
				return err
			}
			if profile.Spec.Image.Mode == "pull" && !immutableImageDigest.MatchString(profile.Spec.Image.Reference) {
				t.Errorf("%s has an unpinned pull image: %s", path, profile.Spec.Image.Reference)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
