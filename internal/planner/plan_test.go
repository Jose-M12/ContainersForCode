package planner

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"containersagents.dev/v2/internal/capability"
	"containersagents.dev/v2/internal/hostinfo"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/policy"
	"containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/resources"
	"containersagents.dev/v2/internal/state"
)

type planRunner struct{ calls [][]string }

func (r *planRunner) Available() error { return nil }
func (r *planRunner) Interactive(context.Context, io.Reader, io.Writer, io.Writer, ...string) error {
	return nil
}
func (r *planRunner) Run(_ context.Context, args ...string) (podman.Result, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	switch {
	case strings.HasPrefix(joined, "container ps"):
		return podman.Result{Stdout: "[]"}, nil
	case strings.HasPrefix(joined, "image exists"):
		return podman.Result{}, nil
	case strings.HasPrefix(joined, "container inspect"):
		return podman.Result{}, &podman.CommandError{Operation: "container", Stderr: "no such container", Err: fmt.Errorf("exit 1")}
	default:
		return podman.Result{}, fmt.Errorf("unexpected command: %s", joined)
	}
}

func TestSandboxPlanUsesSafeArguments(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := state.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"), State: filepath.Join(root, "state"), Cache: filepath.Join(root, "cache")}
	profileManifest := manifest.Profile{APIVersion: manifest.APIVersion, Kind: manifest.ProfileKind, Metadata: manifest.Metadata{Name: "test-profile"}, Spec: manifest.ProfileSpec{Image: manifest.ImageSpec{Mode: "existing", Reference: "registry.example.test/image@sha256:abc", PullPolicy: "never"}, Runtime: manifest.RuntimeSpec{User: "agent", UID: 1000, GID: 1000, Home: "/home/agent", Workdir: "/workspace", Shell: []string{"/bin/sh"}, Keepalive: []string{"sleep", "infinity"}, IdentityMode: "managed-user"}, Defaults: manifest.ProfileDefaults{SecurityClass: "sandbox", ResourceClass: "balanced", RootFSPersistence: "read-only"}}}
	environment := manifest.Environment{APIVersion: manifest.APIVersion, Kind: manifest.EnvironmentKind, Metadata: manifest.Metadata{Name: "test-env"}, Spec: manifest.EnvironmentSpec{Profile: "test-profile", Project: &manifest.ProjectSpec{Source: project, Target: "/workspace", Mode: "read-write"}, SecurityClass: "sandbox", ResourceClass: "balanced", Network: manifest.NetworkSpec{Mode: "outbound"}}}
	runner := &planRunner{}
	builder := Builder{Paths: paths, Podman: podman.Adapter{Runner: runner}, Capabilities: capability.Report{Rootless: true, ResourceLimits: true}, Host: hostinfo.Resources{LogicalCPUs: 8, TotalMemoryBytes: 16 * resources.GiB}, Defaults: manifest.BuiltinDefaults(), UID: -1, Home: home}
	stored := state.EnvironmentState{Schema: 1, ID: "12345678-1234-4abc-8def-123456789abc", Name: "test-env"}
	plan, err := builder.Build(context.Background(), environment, profile.Resolved{Manifest: profileManifest, Hash: strings.Repeat("a", 64), ImageReference: profileManifest.Spec.Image.Reference}, stored, true, Options{})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(plan.CreateArgs, " ")
	for _, required := range []string{"--read-only", "--cap-drop=all", "--security-opt=no-new-privileges", "--network none", "io.containersagents.v2.managed=true"} {
		if !strings.Contains(args, required) {
			t.Errorf("missing %q in %s", required, args)
		}
	}
	for _, forbidden := range []string{"--privileged", "--network host", "/var/run/docker.sock", "/run/user/"} {
		if strings.Contains(args, forbidden) {
			t.Errorf("forbidden %q in %s", forbidden, args)
		}
	}
	if len(plan.Mounts) != 1 || plan.Mounts[0].Mode != "read-only" {
		t.Fatalf("sandbox did not downgrade project mount: %#v", plan.Mounts)
	}
}

func TestProtectedContainerTargetRejected(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project")
	_ = os.Mkdir(project, 0700)
	root := t.TempDir()
	runner := &planRunner{}
	profileManifest := manifest.Profile{Metadata: manifest.Metadata{Name: "test-profile"}, Spec: manifest.ProfileSpec{Image: manifest.ImageSpec{Mode: "existing"}, Runtime: manifest.RuntimeSpec{Home: "/home/agent", Workdir: "/workspace"}, Defaults: manifest.ProfileDefaults{SecurityClass: "development", ResourceClass: "balanced", RootFSPersistence: "persistent"}}}
	environment := manifest.Environment{APIVersion: manifest.APIVersion, Kind: manifest.EnvironmentKind, Metadata: manifest.Metadata{Name: "test-env"}, Spec: manifest.EnvironmentSpec{Profile: "test-profile", Project: &manifest.ProjectSpec{Source: project, Target: "/etc", Mode: "read-only"}, SecurityClass: "development", ResourceClass: "balanced", Network: manifest.NetworkSpec{Mode: "outbound"}, Home: manifest.HomeSpec{Persistence: "per-environment"}, RootFS: manifest.RootFSSpec{Persistence: "persistent"}, Concurrency: manifest.ConcurrencySpec{ProjectMode: "exclusive"}}}
	builder := Builder{Paths: state.Paths{Config: filepath.Join(root, "c"), Data: filepath.Join(root, "d"), State: filepath.Join(root, "s"), Cache: filepath.Join(root, "x")}, Podman: podman.Adapter{Runner: runner}, Capabilities: capability.Report{Rootless: true, ResourceLimits: true}, Host: hostinfo.Resources{LogicalCPUs: 8, TotalMemoryBytes: 16 * resources.GiB}, Defaults: manifest.BuiltinDefaults(), UID: -1, Home: home}
	_, err := builder.Build(context.Background(), environment, profile.Resolved{Manifest: profileManifest, Hash: strings.Repeat("a", 64), ImageReference: "example"}, state.EnvironmentState{ID: "12345678-1234-4abc-8def-123456789abc", Name: "test-env"}, true, Options{})
	if err == nil || !strings.Contains(err.Error(), "protected container path") {
		t.Fatalf("expected target rejection, got %v", err)
	}
}

func TestCreateArgsBracketIPv6PublishAddress(t *testing.T) {
	plan := Plan{
		Environment:   "test-env",
		EnvironmentID: "12345678-1234-4abc-8def-123456789abc",
		ContainerName: "ca2-test-env-12345678",
		Profile:       "test-profile",
		ProfileHash:   strings.Repeat("a", 64),
		SpecHash:      strings.Repeat("b", 64),
		Ports:         []manifest.PortSpec{{HostIP: "::1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}},
		Resources:     resources.Budget{Class: "balanced", MemoryBytes: resources.GiB, SwapBytes: 2 * resources.GiB, CPUs: 1, PIDs: 100, SHMBytes: 64 * 1024 * 1024},
		Security:      policy.SecurityPlan{Class: "integration", HomeAllowed: true, NetworkMode: "integration"},
		EffectiveManifest: manifest.Environment{Spec: manifest.EnvironmentSpec{
			Home: manifest.HomeSpec{Persistence: "none"},
		}},
	}
	resolved := profile.Resolved{Manifest: manifest.Profile{Spec: manifest.ProfileSpec{
		Image:   manifest.ImageSpec{Mode: "existing"},
		Runtime: manifest.RuntimeSpec{Home: "/home/agent", Workdir: "/workspace", Keepalive: []string{"sleep", "infinity"}},
	}}, ImageReference: "registry.example.test/image:latest"}
	args := strings.Join(createArgs(plan, resolved, capability.Report{}, state.Paths{}), " ")
	if !strings.Contains(args, "--publish [::1]:8080:80/tcp") {
		t.Fatalf("IPv6 publish address was not bracketed: %s", args)
	}
}
