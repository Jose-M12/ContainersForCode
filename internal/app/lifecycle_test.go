package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"containersagents.dev/v2/internal/envstore"
	"containersagents.dev/v2/internal/manifest"
	"containersagents.dev/v2/internal/planner"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/policy"
	"containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/resources"
	"containersagents.dev/v2/internal/state"
)

type lifecycleRunner struct {
	run              func([]string) (podman.Result, error)
	interactive      func([]string) error
	calls            [][]string
	interactiveCalls [][]string
}

func (r *lifecycleRunner) Available() error { return nil }

func (r *lifecycleRunner) Run(_ context.Context, args ...string) (podman.Result, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.run == nil {
		return podman.Result{}, fmt.Errorf("unexpected Podman command: %s", strings.Join(args, " "))
	}
	return r.run(args)
}

func (r *lifecycleRunner) Interactive(_ context.Context, _ io.Reader, _, _ io.Writer, args ...string) error {
	r.interactiveCalls = append(r.interactiveCalls, append([]string(nil), args...))
	if r.interactive != nil {
		return r.interactive(args)
	}
	return nil
}

func testApplication(t *testing.T, runner podman.Runner) *Application {
	t.Helper()
	root := t.TempDir()
	paths := state.Paths{
		Config: filepath.Join(root, "config"),
		Data:   filepath.Join(root, "data"),
		State:  filepath.Join(root, "state"),
		Cache:  filepath.Join(root, "cache"),
	}
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	return &Application{
		stdin: strings.NewReader(""), stdout: &output, stderr: &output, paths: paths,
		states: state.Store{Paths: paths}, envs: envstore.Store{Paths: paths}, profiles: profile.Store{Paths: paths},
		runner: runner, podman: podman.Adapter{Runner: runner},
	}
}

func baseLifecyclePlan(stored state.EnvironmentState) planner.Plan {
	return planner.Plan{
		Environment: "demo", EnvironmentID: stored.ID, Profile: "test-profile",
		ProfileHash: "profile-hash", SpecHash: "spec-hash", ProjectHash: "project-hash",
		ImageReference: "registry.example/test@sha256:immutable", ContainerName: "ca2-demo",
		CurrentState: "ABSENT", DesiredState: "RUNNING", ImageExists: false,
		CreateArgs: []string{"container", "create", "--name", "ca2-demo", "registry.example/test@sha256:immutable"},
		Resources:  resources.Budget{Class: "battery", MemoryBytes: 512 * resources.MiB},
		Security:   policy.SecurityPlan{Class: "sandbox", HomeAllowed: false},
		Actions:    []planner.Action{{Type: "pull-image"}, {Type: "create-container"}, {Type: "start-container"}},
		Snapshot:   json.RawMessage(`{"version":1}`),
		EffectiveManifest: manifest.Environment{Spec: manifest.EnvironmentSpec{
			RootFS: manifest.RootFSSpec{Persistence: "ephemeral"},
		}},
	}
}

func pullProfile() profile.Resolved {
	return profile.Resolved{
		ImageReference: "registry.example/test@sha256:immutable",
		Manifest: manifest.Profile{Metadata: manifest.Metadata{Name: "test-profile"}, Spec: manifest.ProfileSpec{
			Image: manifest.ImageSpec{Mode: "pull", Reference: "registry.example/test@sha256:immutable", PullPolicy: "missing"},
		}},
	}
}

func TestApplyPlanPullsCreatesStartsPersistsAndAudits(t *testing.T) {
	runner := &lifecycleRunner{}
	a := testApplication(t, runner)
	stored, err := a.states.Register("demo")
	if err != nil {
		t.Fatal(err)
	}
	plan := baseLifecyclePlan(stored)
	plan.NetworkName = "ca2-network"

	// Match the dynamic environment UUID without weakening assertions on the
	// rest of the network-create command.
	runner.run = func(args []string) (podman.Result, error) {
		joined := strings.Join(args, " ")
		switch {
		case joined == "pull --policy missing registry.example/test@sha256:immutable":
			return podman.Result{}, nil
		case joined == "network inspect ca2-network":
			return podman.Result{}, &podman.CommandError{Operation: "network", Stderr: "no such network", Err: errors.New("missing")}
		case joined == strings.Join(planner.NetworkCreateArgs(plan), " "):
			return podman.Result{}, nil
		case joined == "container create --name ca2-demo registry.example/test@sha256:immutable":
			return podman.Result{Stdout: "container-id\n"}, nil
		case joined == "container start ca2-demo":
			return podman.Result{}, nil
		default:
			return podman.Result{}, fmt.Errorf("unexpected Podman command: %s", joined)
		}
	}

	got, err := a.applyPlan(context.Background(), plan, pullProfile(), stored, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContainerID != "container-id" || got.ContainerName != "ca2-demo" || got.NetworkName != "ca2-network" || got.ProfileHash != "profile-hash" || got.SpecHash != "spec-hash" || got.MemoryBytes != 512*resources.MiB {
		t.Fatalf("unexpected applied state: %#v", got)
	}
	saved, err := a.states.LoadByID(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]int
	if err := json.Unmarshal(saved.AppliedSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if saved.ContainerID != "container-id" || snapshot["version"] != 1 {
		t.Fatalf("state was not persisted: %#v", saved)
	}
	auditData, err := os.ReadFile(a.paths.EnvironmentAuditFile(stored.ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(auditData, []byte(`"command":"apply"`)) || !bytes.Contains(auditData, []byte(`"result":"success"`)) {
		t.Fatalf("audit record is incomplete: %s", auditData)
	}
}

func TestApplyPlanFailsClosedForDriftAndUnavailableInputs(t *testing.T) {
	a := testApplication(t, &lifecycleRunner{})
	stored, err := a.states.Register("demo")
	if err != nil {
		t.Fatal(err)
	}

	drifted := baseLifecyclePlan(stored)
	drifted.CurrentState = "DRIFTED"
	if _, err := a.applyPlan(context.Background(), drifted, pullProfile(), stored, true); err == nil || ExitCode(err) != 3 {
		t.Fatalf("expected drift policy rejection, got %v", err)
	}

	existing := baseLifecyclePlan(stored)
	resolved := pullProfile()
	resolved.Manifest.Spec.Image.Mode = "existing"
	if _, err := a.applyPlan(context.Background(), existing, resolved, stored, false); err == nil || ExitCode(err) != 4 {
		t.Fatalf("expected unavailable existing-image rejection, got %v", err)
	}

	buildSecret := baseLifecyclePlan(stored)
	resolved = pullProfile()
	resolved.Manifest.Spec.Image.Mode = "build"
	resolved.Manifest.Spec.Image.BuildSecrets = []manifest.BuildSecret{{Name: "token", ID: "token"}}
	if _, err := a.applyPlan(context.Background(), buildSecret, resolved, stored, false); err == nil || ExitCode(err) != 3 {
		t.Fatalf("expected build-secret provider rejection, got %v", err)
	}
}

func TestCleanupAfterOwnedEphemeralSessionStopsRemovesAndClearsState(t *testing.T) {
	runner := &lifecycleRunner{run: func(args []string) (podman.Result, error) {
		switch strings.Join(args, " ") {
		case "container stop --time 10 ca2-demo", "container rm ca2-demo":
			return podman.Result{}, nil
		default:
			return podman.Result{}, fmt.Errorf("unexpected Podman command: %s", strings.Join(args, " "))
		}
	}}
	a := testApplication(t, runner)
	stored, err := a.states.Register("demo")
	if err != nil {
		t.Fatal(err)
	}
	stored.ContainerID, stored.ContainerName = "container-id", "ca2-demo"
	if err := a.states.Save(stored); err != nil {
		t.Fatal(err)
	}
	session := &activeSession{
		application: a, plan: planner.Plan{ContainerName: "ca2-demo"}, state: stored, started: true,
		environment: manifest.Environment{Spec: manifest.EnvironmentSpec{RootFS: manifest.RootFSSpec{Persistence: "ephemeral"}}},
		defaults:    manifest.BuiltinDefaults(),
	}
	if err := session.cleanupAfterOwnedSession(); err != nil {
		t.Fatal(err)
	}
	saved, err := a.states.LoadByID(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ContainerID != "" || saved.ContainerName != "" {
		t.Fatalf("ephemeral container identity was retained: %#v", saved)
	}
	wantCalls := []string{"container stop --time 10 ca2-demo", "container rm ca2-demo"}
	if got := joinedCalls(runner.calls); !reflectStrings(got, wantCalls) {
		t.Fatalf("got calls %v, want %v", got, wantCalls)
	}
}

func TestCleanupHonorsSessionOwnershipAndStopPreference(t *testing.T) {
	stop := false
	for _, session := range []*activeSession{
		{started: false},
		{started: true, defaults: manifest.Defaults{Spec: manifest.DefaultsSpec{StopOnShellExit: &stop}}},
	} {
		runner := &lifecycleRunner{}
		a := testApplication(t, runner)
		session.application = a
		if err := session.cleanupAfterOwnedSession(); err != nil {
			t.Fatal(err)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("cleanup touched an unowned/retained session: %v", runner.calls)
		}
	}
}

func TestProjectConcurrencyModes(t *testing.T) {
	const otherID = "other-environment"
	newApplication := func(t *testing.T) *Application {
		runner := &lifecycleRunner{run: func(args []string) (podman.Result, error) {
			if strings.HasPrefix(strings.Join(args, " "), "container ps ") {
				return podman.Result{Stdout: `[{"Id":"other","Names":"other","State":"running","Labels":{"` + podman.EnvironmentIDLabel + `":"` + otherID + `","` + podman.ProjectHashLabel + `":"project-hash"}}]`}, nil
			}
			return podman.Result{}, fmt.Errorf("unexpected Podman command: %s", strings.Join(args, " "))
		}}
		return testApplication(t, runner)
	}
	plan := planner.Plan{EnvironmentID: "this-environment", ProjectHash: "project-hash"}

	tests := []struct {
		name        string
		mode        string
		projectMode string
		wantError   bool
	}{
		{"exclusive blocks", "exclusive", "read-write", true},
		{"prompt blocks", "prompt", "read-write", true},
		{"read-only-secondary blocks writers", "read-only-secondary", "read-write", true},
		{"read-only-secondary permits readers", "read-only-secondary", "read-only", false},
		{"allow permits another writer", "allow", "read-write", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := newApplication(t)
			environment := manifest.Environment{Spec: manifest.EnvironmentSpec{
				Project: &manifest.ProjectSpec{Mode: test.projectMode}, Concurrency: manifest.ConcurrencySpec{ProjectMode: test.mode},
			}}
			err := a.enforceProjectConcurrency(context.Background(), plan, environment)
			if (err != nil) != test.wantError {
				t.Fatalf("got err=%v, wantError=%t", err, test.wantError)
			}
		})
	}
}

func TestEnvironmentStopVerifiesOwnershipAndRemovesEphemeralContainer(t *testing.T) {
	var environmentID string
	runner := &lifecycleRunner{run: func(args []string) (podman.Result, error) {
		switch strings.Join(args, " ") {
		case "container inspect ca2-demo":
			return podman.Result{Stdout: `[{"Id":"container-id","Name":"ca2-demo","Config":{"Labels":{"` + podman.ManagedLabel + `":"true","` + podman.EnvironmentIDLabel + `":"` + environmentID + `"}},"State":{"Running":true,"Status":"running"}}]`}, nil
		case "container stop --time 10 ca2-demo", "container rm ca2-demo":
			return podman.Result{}, nil
		default:
			return podman.Result{}, fmt.Errorf("unexpected Podman command: %s", strings.Join(args, " "))
		}
	}}
	a := testApplication(t, runner)
	environment := manifest.Environment{
		APIVersion: manifest.APIVersion, Kind: manifest.EnvironmentKind, Metadata: manifest.Metadata{Name: "demo"},
		Spec: manifest.EnvironmentSpec{
			Profile: "nix-cli", Home: manifest.HomeSpec{Persistence: "none"}, RootFS: manifest.RootFSSpec{Persistence: "ephemeral"},
			SecurityClass: "sandbox", ResourceClass: "battery", Concurrency: manifest.ConcurrencySpec{ProjectMode: "exclusive"}, Network: manifest.NetworkSpec{Mode: "none"},
		},
	}
	if err := a.envs.Save(environment); err != nil {
		t.Fatal(err)
	}
	stored, err := a.states.Register("demo")
	if err != nil {
		t.Fatal(err)
	}
	environmentID = stored.ID
	stored.ContainerID, stored.ContainerName = "container-id", "ca2-demo"
	if err := a.states.Save(stored); err != nil {
		t.Fatal(err)
	}
	if err := a.environmentStop(context.Background(), []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	saved, err := a.states.LoadByID(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ContainerID != "" || saved.ContainerName != "" {
		t.Fatalf("stop retained ephemeral state: %#v", saved)
	}
}

func joinedCalls(calls [][]string) []string {
	result := make([]string, len(calls))
	for i, call := range calls {
		result[i] = strings.Join(call, " ")
	}
	return result
}

func reflectStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
