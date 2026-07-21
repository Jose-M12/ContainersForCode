package state

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"containersagents.dev/v2/internal/fsutil"
)

const applicationName = "containersagents-v2"

type Paths struct {
	Config string `json:"config"`
	Data   string `json:"data"`
	State  string `json:"state"`
	Cache  string `json:"cache"`
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user home: %w", err)
	}
	config, err := categoryPath("XDG_CONFIG_HOME", filepath.Join(home, ".config"), applicationName)
	if err != nil {
		return Paths{}, err
	}
	dataDefault := filepath.Join(home, ".local", "share")
	stateDefault := filepath.Join(home, ".local", "state")
	cacheDefault := filepath.Join(home, ".cache")
	if runtime.GOOS == "darwin" {
		dataDefault = filepath.Join(home, "Library", "Application Support")
		stateDefault = filepath.Join(home, "Library", "Application Support")
		cacheDefault = filepath.Join(home, "Library", "Caches")
	} else if runtime.GOOS == "windows" {
		if configDir, configErr := os.UserConfigDir(); configErr == nil {
			config = filepath.Join(configDir, applicationName)
			dataDefault = configDir
			stateDefault = configDir
		}
		if cacheDir, cacheErr := os.UserCacheDir(); cacheErr == nil {
			cacheDefault = cacheDir
		}
	}
	data, err := categoryPath("XDG_DATA_HOME", dataDefault, applicationName)
	if err != nil {
		return Paths{}, err
	}
	stateRoot, err := categoryPath("XDG_STATE_HOME", stateDefault, applicationName)
	if err != nil {
		return Paths{}, err
	}
	if runtime.GOOS == "darwin" && os.Getenv("XDG_STATE_HOME") == "" {
		stateRoot = filepath.Join(stateRoot, "state")
	} else if runtime.GOOS == "windows" && os.Getenv("XDG_STATE_HOME") == "" {
		data = filepath.Join(data, "data")
		stateRoot = filepath.Join(stateRoot, "state")
	}
	cache, err := categoryPath("XDG_CACHE_HOME", cacheDefault, applicationName)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Config: config, Data: data, State: stateRoot, Cache: cache}, nil
}

func categoryPath(variable, fallback, child string) (string, error) {
	root := os.Getenv(variable)
	if root == "" {
		root = fallback
	} else if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s must be an absolute path when set", variable)
	}
	return filepath.Clean(filepath.Join(root, child)), nil
}

func (p Paths) DefaultsFile() string    { return filepath.Join(p.Config, "defaults.json") }
func (p Paths) ProfilesDir() string     { return filepath.Join(p.Config, "profiles") }
func (p Paths) EnvironmentsDir() string { return filepath.Join(p.Config, "environments") }
func (p Paths) EnvironmentManifest(name string) string {
	return filepath.Join(p.EnvironmentsDir(), name+".json")
}
func (p Paths) EnvironmentData(id string) string  { return filepath.Join(p.Data, "environments", id) }
func (p Paths) EnvironmentHome(id string) string  { return filepath.Join(p.EnvironmentData(id), "home") }
func (p Paths) EnvironmentState(id string) string { return filepath.Join(p.State, "environments", id) }
func (p Paths) EnvironmentStateFile(id string) string {
	return filepath.Join(p.EnvironmentState(id), "state.json")
}
func (p Paths) EnvironmentAuditFile(id string) string {
	return filepath.Join(p.EnvironmentState(id), "audit.jsonl")
}
func (p Paths) IndexFile() string  { return filepath.Join(p.State, "environments", "index.json") }
func (p Paths) GlobalLock() string { return filepath.Join(p.State, "global.lock") }
func (p Paths) EnvironmentLock(id string) string {
	return filepath.Join(p.EnvironmentState(id), "locks", "lifecycle.lock")
}
func (p Paths) ProjectLock(hash string) string {
	return filepath.Join(p.State, "project-locks", hash+".lock")
}
func (p Paths) ProfileBuildLock(name string) string {
	return filepath.Join(p.State, "profile-locks", name+".lock")
}
func (p Paths) CapabilityCache() string { return filepath.Join(p.Cache, "capabilities.json") }
func (p Paths) ProfileBuildCache(hash string) string {
	return filepath.Join(p.Cache, "profile-builds", hash)
}

func (p Paths) Ensure() error {
	for _, path := range []string{p.Config, p.Data, p.State, p.Cache, p.ProfilesDir(), p.EnvironmentsDir(), filepath.Join(p.State, "environments")} {
		if err := fsutil.EnsureDir(path, 0700); err != nil {
			return err
		}
	}
	return nil
}
