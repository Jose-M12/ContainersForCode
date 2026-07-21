package envstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"containersagents.dev/v2/internal/fsutil"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/state"
)

type Store struct{ Paths state.Paths }

func (s Store) Load(name string) (manifest.Environment, error) {
	if err := manifest.ValidateName(name); err != nil {
		return manifest.Environment{}, fmt.Errorf("invalid environment name %q: %w", name, err)
	}
	environment, err := manifest.LoadEnvironment(s.Paths.EnvironmentManifest(name))
	if err != nil {
		return manifest.Environment{}, err
	}
	if environment.Metadata.Name != name {
		return manifest.Environment{}, fmt.Errorf("environment filename %q does not match metadata.name %q", name, environment.Metadata.Name)
	}
	return environment, nil
}

func (s Store) Save(environment manifest.Environment) error {
	if err := manifest.ValidateEnvironment(environment); err != nil {
		return err
	}
	path := s.Paths.EnvironmentManifest(environment.Metadata.Name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("environment %q already exists", environment.Metadata.Name)
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(environment, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(path, append(data, '\n'), 0600)
}

func (s Store) Update(environment manifest.Environment) error {
	if err := manifest.ValidateEnvironment(environment); err != nil {
		return err
	}
	data, err := json.MarshalIndent(environment, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.AtomicWrite(s.Paths.EnvironmentManifest(environment.Metadata.Name), append(data, '\n'), 0600)
}

func (s Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.Paths.EnvironmentsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		if manifest.ValidateName(name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
