//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootlessPodmanRawRun(t *testing.T) {
	if os.Getenv("CAGENT_RUN_PODMAN_INTEGRATION") != "1" {
		t.Skip("set CAGENT_RUN_PODMAN_INTEGRATION=1 to run Podman integration tests")
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Fatal("Podman integration requested, but podman is not on PATH")
	}

	info := exec.Command("podman", "info", "--format", "json")
	output, err := info.CombinedOutput()
	if err != nil {
		t.Fatalf("podman info: %v\n%s", err, output)
	}
	rootless, cgroupVersion, err := decodePodmanInfo(output)
	if err != nil {
		t.Fatalf("decode podman info: %v\n%s", err, output)
	}
	if !rootless {
		t.Fatal("integration tests require rootless Podman; Podman reports rootless=false")
	}
	if cgroupVersion != "v2" && cgroupVersion != "2" {
		t.Fatalf("integration tests require cgroups v2; got %q", cgroupVersion)
	}

	binary, err := filepath.Abs(filepath.Join("..", "..", "bin", "cagent"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("cagent binary is unavailable at %s; run make build first: %v", binary, err)
	}

	root, err := os.MkdirTemp("", "cagent-integration-")
	if err != nil {
		t.Fatalf("create integration root: %v", err)
	}
	runtimeDirectory := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeDirectory, 0700); err != nil {
		t.Fatalf("create integration runtime directory: %v", err)
	}
	baseEnv := append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
		"XDG_DATA_HOME="+filepath.Join(root, "data"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"XDG_RUNTIME_DIR="+runtimeDirectory,
	)
	t.Cleanup(func() {
		// Podman must unmount rootless overlay storage before Go can remove it.
		// Every XDG storage and runtime path is under this random test root, so
		// the reset cannot affect the user's normal Podman storage.
		command := exec.Command("podman", "system", "reset", "--force")
		command.Env = baseEnv
		if output, cleanupErr := command.CombinedOutput(); cleanupErr != nil {
			t.Errorf("reset isolated Podman storage at %s: %v\n%s", root, cleanupErr, output)
		}
		if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
			t.Errorf("remove integration root %s: %v", root, cleanupErr)
		}
	})

	version := exec.Command(binary, "version", "--output", "json")
	version.Env = baseEnv
	versionOutput, err := version.CombinedOutput()
	if err != nil {
		t.Fatalf("cagent version: %v\n%s", err, versionOutput)
	}
	var versionValue map[string]string
	if err := json.Unmarshal(versionOutput, &versionValue); err != nil {
		t.Fatalf("version output is not JSON: %v\n%s", err, versionOutput)
	}
	if versionValue["version"] == "" {
		t.Fatal("version output omitted version")
	}

	image := os.Getenv("CAGENT_INTEGRATION_IMAGE")
	if image == "" {
		image = "docker.io/library/alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce"
	}
	run := exec.Command(binary, "run", "--image", image, "--network", "none", "--tty=false", "--", "/bin/true")
	run.Env = baseEnv
	runOutput, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("raw image smoke test with %s: %v\n%s", image, err, runOutput)
	}

	writeIntegrationProfile(t, root, image)
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "integration-marker"), []byte("managed-lifecycle-ok\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeIntegrationDefaults(t, root, project)

	const environmentName = "integration-smoke"
	t.Cleanup(func() {
		command := exec.Command(binary, "env", "delete", environmentName, "--confirm", environmentName)
		command.Env = baseEnv
		_, _ = command.CombinedOutput()
	})
	runCagent(t, binary, baseEnv, "env", "init", environmentName,
		"--profile", "integration-alpine", "--project", project,
		"--security", "sandbox", "--resource", "battery", "--rootfs", "ephemeral")

	initial := runCagentJSON(t, binary, baseEnv, "env", "plan", environmentName, "--output", "json")
	if boolField(t, initial, "containerExists") {
		t.Fatal("new integration environment unexpectedly has a container")
	}

	runCagent(t, binary, baseEnv, "env", "prepare", environmentName)
	prepared := runCagentJSON(t, binary, baseEnv, "env", "plan", environmentName, "--output", "json")
	if !boolField(t, prepared, "containerExists") || boolField(t, prepared, "containerRunning") {
		t.Fatalf("prepare did not leave a stopped container: %v", prepared)
	}

	runCagent(t, binary, baseEnv, "env", "start", environmentName)
	running := runCagentJSON(t, binary, baseEnv, "env", "plan", environmentName, "--output", "json")
	if !boolField(t, running, "containerExists") || !boolField(t, running, "containerRunning") {
		t.Fatalf("start did not leave a running container: %v", running)
	}
	execOutput := runCagent(t, binary, baseEnv, "env", "exec", environmentName, "--", "/bin/cat", "/workspace/integration-marker")
	if strings.TrimSpace(string(execOutput)) != "managed-lifecycle-ok" {
		t.Fatalf("project mount was not readable in the managed environment: %q", execOutput)
	}
	doctor := runCagentJSONValue(t, binary, baseEnv, "env", "doctor", environmentName, "--output", "json")
	checks, ok := doctor.([]any)
	if !ok || len(checks) != 1 {
		t.Fatalf("unexpected doctor output: %#v", doctor)
	}
	check, ok := checks[0].(map[string]any)
	if !ok || check["success"] != true {
		t.Fatalf("managed profile health check failed: %#v", doctor)
	}

	runCagent(t, binary, baseEnv, "env", "stop", environmentName)
	stopped := runCagentJSON(t, binary, baseEnv, "env", "plan", environmentName, "--output", "json")
	if boolField(t, stopped, "containerExists") {
		t.Fatalf("ephemeral stop retained the container: %v", stopped)
	}
	runCagent(t, binary, baseEnv, "env", "delete", environmentName, "--confirm", environmentName)
	listed := runCagentJSONValue(t, binary, baseEnv, "env", "list", "--output", "json")
	if values, ok := listed.([]any); !ok || len(values) != 0 {
		t.Fatalf("deleted environment remains listed: %#v", listed)
	}
}

func writeIntegrationDefaults(t *testing.T, root, project string) {
	t.Helper()
	directory := filepath.Join(root, "config", "containersagents-v2")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	defaults := map[string]any{
		"apiVersion": "containersagents.dev/v2alpha1",
		"kind":       "Defaults",
		"spec": map[string]any{
			"allowedMountRoots": []string{project},
			"strictResources":   true,
		},
	}
	data, err := json.MarshalIndent(defaults, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "defaults.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeIntegrationProfile(t *testing.T, root, image string) {
	t.Helper()
	directory := filepath.Join(root, "config", "containersagents-v2", "profiles", "integration-alpine")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	profile := map[string]any{
		"apiVersion": "containersagents.dev/v2alpha1",
		"kind":       "Profile",
		"metadata":   map[string]any{"name": "integration-alpine"},
		"spec": map[string]any{
			"image": map[string]any{"mode": "pull", "reference": image, "pullPolicy": "missing"},
			"runtime": map[string]any{
				"user": "root", "home": "/root", "workdir": "/workspace",
				"shell": []string{"/bin/sh"}, "keepalive": []string{"sleep", "infinity"},
				"identityMode": "rootless-container-root",
			},
			"defaults": map[string]any{"securityClass": "sandbox", "resourceClass": "battery", "rootfsPersistence": "ephemeral"},
			"checks":   []any{map[string]any{"name": "shell", "command": []string{"/bin/sh", "-c", "test -r /etc/alpine-release"}, "timeoutSeconds": 10}},
		},
	}
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "profile.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func runCagent(t *testing.T, binary string, environment []string, args ...string) []byte {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("cagent %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func runCagentJSON(t *testing.T, binary string, environment []string, args ...string) map[string]any {
	t.Helper()
	value := runCagentJSONValue(t, binary, environment, args...)
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("cagent %s returned %T, want JSON object", strings.Join(args, " "), value)
	}
	return object
}

func runCagentJSONValue(t *testing.T, binary string, environment []string, args ...string) any {
	t.Helper()
	output := runCagent(t, binary, environment, args...)
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("cagent %s returned invalid JSON: %v\n%s", strings.Join(args, " "), err, output)
	}
	return value
}

func boolField(t *testing.T, value map[string]any, name string) bool {
	t.Helper()
	field, ok := value[name].(bool)
	if !ok {
		t.Fatalf("field %q is %T, want bool in %s", name, value[name], fmt.Sprint(value))
	}
	return field
}
