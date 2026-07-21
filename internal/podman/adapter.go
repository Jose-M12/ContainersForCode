package podman

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

const (
	ManagedLabel       = "io.containersagents.v2.managed"
	EnvironmentIDLabel = "io.containersagents.v2.environment-id"
	ProfileHashLabel   = "io.containersagents.v2.profile-hash"
	SpecHashLabel      = "io.containersagents.v2.spec-hash"
	ProjectHashLabel   = "io.containersagents.v2.project-hash"
)

type Adapter struct {
	Runner Runner
}

type Container struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	State  string            `json:"state"`
	Labels map[string]string `json:"labels"`
}

type ContainerInspect struct {
	ID       string
	Name     string
	Running  bool
	Status   string
	Labels   map[string]string
	Memory   int64
	Image    string
	ExitCode int
}

type Image struct {
	ID         string            `json:"id"`
	Repository string            `json:"repository,omitempty"`
	Tag        string            `json:"tag,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Size       int64             `json:"size,omitempty"`
}

type Network struct {
	Name   string            `json:"name"`
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels,omitempty"`
}

func (a Adapter) ImageExists(ctx context.Context, reference string) (bool, error) {
	_, err := a.Runner.Run(ctx, "image", "exists", reference)
	if err == nil {
		return true, nil
	}
	var commandErr *CommandError
	if errorsAs(err, &commandErr) {
		var exitErr *exec.ExitError
		if errors.As(commandErr.Err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
	}
	return false, err
}

func (a Adapter) SecretExists(ctx context.Context, name string) (bool, error) {
	_, err := a.Runner.Run(ctx, "secret", "inspect", name)
	if err == nil {
		return true, nil
	}
	var commandErr *CommandError
	if errorsAs(err, &commandErr) && strings.Contains(strings.ToLower(commandErr.Stderr), "no such") {
		return false, nil
	}
	return false, err
}

func (a Adapter) NetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := a.Runner.Run(ctx, "network", "inspect", name)
	if err == nil {
		return true, nil
	}
	var commandErr *CommandError
	if errorsAs(err, &commandErr) && strings.Contains(strings.ToLower(commandErr.Stderr), "no such") {
		return false, nil
	}
	return false, err
}

func (a Adapter) InspectNetwork(ctx context.Context, name string) (Network, bool, error) {
	result, err := a.Runner.Run(ctx, "network", "inspect", name)
	if err != nil {
		var commandErr *CommandError
		if errorsAs(err, &commandErr) && strings.Contains(strings.ToLower(commandErr.Stderr), "no such") {
			return Network{}, false, nil
		}
		return Network{}, false, err
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return Network{}, false, fmt.Errorf("decode Podman network inspect JSON: %w", err)
	}
	if len(raw) != 1 {
		return Network{}, false, fmt.Errorf("Podman returned %d network inspect records for %q", len(raw), name)
	}
	return Network{Name: stringValue(raw[0], "name", "Name"), ID: stringValue(raw[0], "id", "Id", "ID"), Labels: stringMap(firstValue(raw[0], "labels", "Labels"))}, true, nil
}

func (a Adapter) Pull(ctx context.Context, reference, policy string) error {
	_, err := a.Runner.Run(ctx, "pull", "--policy", policy, reference)
	return err
}

func (a Adapter) Build(ctx context.Context, args []string) (string, error) {
	result, err := a.Runner.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (a Adapter) Create(ctx context.Context, args []string) (string, error) {
	result, err := a.Runner.Run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (a Adapter) Start(ctx context.Context, name string) error {
	_, err := a.Runner.Run(ctx, "container", "start", name)
	return err
}

func (a Adapter) Stop(ctx context.Context, name string, timeoutSeconds int) error {
	_, err := a.Runner.Run(ctx, "container", "stop", "--time", strconv.Itoa(timeoutSeconds), name)
	return err
}

func (a Adapter) RemoveContainer(ctx context.Context, name string) error {
	_, err := a.Runner.Run(ctx, "container", "rm", name)
	return err
}

func (a Adapter) RemoveImage(ctx context.Context, id string) error {
	_, err := a.Runner.Run(ctx, "image", "rm", id)
	return err
}

func (a Adapter) CreateNetwork(ctx context.Context, args []string) error {
	_, err := a.Runner.Run(ctx, args...)
	return err
}

func (a Adapter) RemoveNetwork(ctx context.Context, name string) error {
	_, err := a.Runner.Run(ctx, "network", "rm", name)
	return err
}

func (a Adapter) ExecInteractive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, command []string) error {
	args := []string{"container", "exec", "--interactive", "--tty", name}
	args = append(args, command...)
	return a.Runner.Interactive(ctx, stdin, stdout, stderr, args...)
}

func (a Adapter) Exec(ctx context.Context, name string, command []string) (Result, error) {
	args := []string{"container", "exec", name}
	args = append(args, command...)
	return a.Runner.Run(ctx, args...)
}

func (a Adapter) RunInteractive(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, args []string) error {
	return a.Runner.Interactive(ctx, stdin, stdout, stderr, args...)
}

func (a Adapter) InspectContainer(ctx context.Context, name string) (ContainerInspect, bool, error) {
	result, err := a.Runner.Run(ctx, "container", "inspect", name)
	if err != nil {
		var commandErr *CommandError
		if errorsAs(err, &commandErr) && strings.Contains(strings.ToLower(commandErr.Stderr), "no such") {
			return ContainerInspect{}, false, nil
		}
		return ContainerInspect{}, false, err
	}
	var values []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Image  string `json:"ImageName"`
		Config struct {
			Labels map[string]string `json:"Labels"`
			Image  string            `json:"Image"`
		} `json:"Config"`
		State struct {
			Running  bool   `json:"Running"`
			Status   string `json:"Status"`
			ExitCode int    `json:"ExitCode"`
		} `json:"State"`
		HostConfig struct {
			Memory int64 `json:"Memory"`
		} `json:"HostConfig"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &values); err != nil {
		return ContainerInspect{}, false, fmt.Errorf("decode Podman container inspect JSON: %w", err)
	}
	if len(values) != 1 {
		return ContainerInspect{}, false, fmt.Errorf("Podman returned %d inspect records for %q", len(values), name)
	}
	value := values[0]
	image := value.Image
	if image == "" {
		image = value.Config.Image
	}
	return ContainerInspect{ID: value.ID, Name: strings.TrimPrefix(value.Name, "/"), Running: value.State.Running, Status: value.State.Status, ExitCode: value.State.ExitCode, Labels: value.Config.Labels, Memory: value.HostConfig.Memory, Image: image}, true, nil
}

func (a Adapter) ListManagedContainers(ctx context.Context, all bool) ([]Container, error) {
	args := []string{"container", "ps"}
	if all {
		args = append(args, "--all")
	}
	args = append(args, "--filter", "label="+ManagedLabel+"=true", "--format", "json")
	result, err := a.Runner.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID     string            `json:"Id"`
		IDAlt  string            `json:"ID"`
		Names  any               `json:"Names"`
		State  string            `json:"State"`
		Status string            `json:"Status"`
		Labels map[string]string `json:"Labels"`
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return []Container{}, nil
	}
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("decode Podman container list JSON: %w", err)
	}
	containers := make([]Container, 0, len(raw))
	for _, value := range raw {
		id := value.ID
		if id == "" {
			id = value.IDAlt
		}
		name := firstName(value.Names)
		state := value.State
		if state == "" {
			state = value.Status
		}
		containers = append(containers, Container{ID: id, Name: name, State: state, Labels: value.Labels})
	}
	return containers, nil
}

func (a Adapter) RunningManagedMemory(ctx context.Context) (int64, error) {
	containers, err := a.ListManagedContainers(ctx, false)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, container := range containers {
		inspect, exists, err := a.InspectContainer(ctx, container.ID)
		if err != nil {
			return 0, err
		}
		if exists && inspect.Running {
			total += inspect.Memory
		}
	}
	return total, nil
}

func (a Adapter) ListManagedImages(ctx context.Context) ([]Image, error) {
	result, err := a.Runner.Run(ctx, "image", "list", "--filter", "label="+ManagedLabel+"=true", "--format", "json")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return []Image{}, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("decode Podman image list JSON: %w", err)
	}
	images := make([]Image, 0, len(raw))
	for _, value := range raw {
		images = append(images, Image{ID: stringValue(value, "Id", "ID"), Repository: stringValue(value, "Repository"), Tag: stringValue(value, "Tag"), Labels: stringMap(value["Labels"]), Size: int64Value(value["Size"])})
	}
	return images, nil
}

func (a Adapter) ListManagedNetworks(ctx context.Context) ([]Network, error) {
	result, err := a.Runner.Run(ctx, "network", "ls", "--filter", "label="+ManagedLabel+"=true", "--format", "json")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return []Network{}, nil
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(result.Stdout), &raw); err != nil {
		return nil, fmt.Errorf("decode Podman network list JSON: %w", err)
	}
	networks := make([]Network, 0, len(raw))
	for _, value := range raw {
		networks = append(networks, Network{Name: stringValue(value, "Name"), ID: stringValue(value, "Id", "ID"), Labels: stringMap(value["Labels"])})
	}
	return networks, nil
}

func VerifyEnvironment(inspect ContainerInspect, environmentID string) error {
	if inspect.Labels[ManagedLabel] != "true" || inspect.Labels[EnvironmentIDLabel] != environmentID {
		return fmt.Errorf("refuse to operate on container %q: V2 ownership labels do not match environment UUID", inspect.Name)
	}
	return nil
}

func firstName(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimPrefix(typed, "/")
	case []any:
		if len(typed) > 0 {
			return strings.TrimPrefix(fmt.Sprint(typed[0]), "/")
		}
	}
	return ""
}

func stringValue(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if found, ok := value[key]; ok {
			return fmt.Sprint(found)
		}
	}
	return ""
}

func firstValue(value map[string]any, keys ...string) any {
	for _, key := range keys {
		if found, ok := value[key]; ok {
			return found
		}
	}
	return nil
}

func stringMap(value any) map[string]string {
	result := map[string]string{}
	if typed, ok := value.(map[string]any); ok {
		for key, item := range typed {
			result[key] = fmt.Sprint(item)
		}
	}
	if typed, ok := value.(map[string]string); ok {
		return typed
	}
	return result
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	}
	return 0
}

// Kept local to avoid exposing a dependency on the errors package in callers.
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
