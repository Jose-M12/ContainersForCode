package state

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"time"

	"containersagents.dev/v2/internal/fsutil"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Index struct {
	Schema       int               `json:"schema"`
	Environments map[string]string `json:"environments"`
}

type EnvironmentState struct {
	Schema          int             `json:"schema"`
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	ContainerName   string          `json:"containerName,omitempty"`
	ContainerID     string          `json:"containerID,omitempty"`
	NetworkName     string          `json:"networkName,omitempty"`
	ProfileHash     string          `json:"profileHash,omitempty"`
	SpecHash        string          `json:"specHash,omitempty"`
	ProjectHash     string          `json:"projectHash,omitempty"`
	ResourceClass   string          `json:"resourceClass,omitempty"`
	SecurityClass   string          `json:"securityClass,omitempty"`
	MemoryBytes     int64           `json:"memoryBytes,omitempty"`
	AppliedSnapshot json.RawMessage `json:"appliedSnapshot,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type Store struct {
	Paths Paths
}

func (s Store) LoadIndex() (Index, error) {
	data, err := os.ReadFile(s.Paths.IndexFile())
	if err != nil {
		if os.IsNotExist(err) {
			return Index{Schema: 1, Environments: map[string]string{}}, nil
		}
		return Index{}, fmt.Errorf("read environment index: %w", err)
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return Index{}, fmt.Errorf("decode environment index: %w", err)
	}
	if index.Schema != 1 || index.Environments == nil {
		return Index{}, fmt.Errorf("environment index has unsupported or invalid schema")
	}
	for name, id := range index.Environments {
		if name == "" || !uuidPattern.MatchString(id) {
			return Index{}, fmt.Errorf("environment index contains invalid entry for %q", name)
		}
	}
	return index, nil
}

func (s Store) SaveIndex(index Index) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode environment index: %w", err)
	}
	data = append(data, '\n')
	return fsutil.AtomicWrite(s.Paths.IndexFile(), data, 0600)
}

func (s Store) Register(name string) (EnvironmentState, error) {
	if err := s.Paths.Ensure(); err != nil {
		return EnvironmentState{}, err
	}
	index, err := s.LoadIndex()
	if err != nil {
		return EnvironmentState{}, err
	}
	if id, exists := index.Environments[name]; exists {
		return s.LoadByID(id)
	}
	id, err := NewUUID()
	if err != nil {
		return EnvironmentState{}, err
	}
	now := time.Now().UTC()
	state := EnvironmentState{Schema: 1, ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
	if err := fsutil.EnsureDir(s.Paths.EnvironmentState(id), 0700); err != nil {
		return EnvironmentState{}, err
	}
	if err := fsutil.EnsureDir(s.Paths.EnvironmentData(id), 0700); err != nil {
		return EnvironmentState{}, err
	}
	if err := s.Save(state); err != nil {
		return EnvironmentState{}, err
	}
	index.Environments[name] = id
	if err := s.SaveIndex(index); err != nil {
		return EnvironmentState{}, err
	}
	return state, nil
}

func (s Store) GetByName(name string) (EnvironmentState, bool, error) {
	index, err := s.LoadIndex()
	if err != nil {
		return EnvironmentState{}, false, err
	}
	id, exists := index.Environments[name]
	if !exists {
		return EnvironmentState{}, false, nil
	}
	state, err := s.LoadByID(id)
	return state, true, err
}

func (s Store) LoadByID(id string) (EnvironmentState, error) {
	if !uuidPattern.MatchString(id) {
		return EnvironmentState{}, fmt.Errorf("invalid environment UUID %q", id)
	}
	data, err := os.ReadFile(s.Paths.EnvironmentStateFile(id))
	if err != nil {
		return EnvironmentState{}, fmt.Errorf("read state for environment %s: %w", id, err)
	}
	var state EnvironmentState
	if err := json.Unmarshal(data, &state); err != nil {
		return EnvironmentState{}, fmt.Errorf("decode state for environment %s: %w", id, err)
	}
	if state.Schema != 1 || state.ID != id || state.Name == "" {
		return EnvironmentState{}, fmt.Errorf("state for environment %s is inconsistent", id)
	}
	return state, nil
}

func (s Store) Save(state EnvironmentState) error {
	if !uuidPattern.MatchString(state.ID) {
		return fmt.Errorf("refuse to save invalid environment UUID %q", state.ID)
	}
	state.Schema = 1
	state.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode environment state: %w", err)
	}
	data = append(data, '\n')
	return fsutil.AtomicWrite(s.Paths.EnvironmentStateFile(state.ID), data, 0600)
}

func (s Store) Names() ([]string, error) {
	index, err := s.LoadIndex()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(index.Environments))
	for name := range index.Environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (s Store) Forget(name string) error {
	index, err := s.LoadIndex()
	if err != nil {
		return err
	}
	if _, exists := index.Environments[name]; !exists {
		return nil
	}
	delete(index.Environments, name)
	return s.SaveIndex(index)
}

func NewUUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate environment UUID: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}
