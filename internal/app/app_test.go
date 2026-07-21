package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"containersagents.dev/v2/internal/envstore"
	"containersagents.dev/v2/internal/podman"
	"containersagents.dev/v2/internal/profile"
	"containersagents.dev/v2/internal/resources"
	"containersagents.dev/v2/internal/state"
)

func TestInterspersedFlags(t *testing.T) {
	flags := newFlagSet("test")
	output := flags.String("output", "human", "")
	force := flags.Bool("force", false, "")
	if err := parseFlags(flags, []string{"resource-name", "--force", "--output", "json"}); err != nil {
		t.Fatal(err)
	}
	if flags.Arg(0) != "resource-name" || !*force || *output != "json" {
		t.Fatalf("unexpected parse: args=%v force=%t output=%s", flags.Args(), *force, *output)
	}
}

type isolationRunner struct{ calls [][]string }

func (r *isolationRunner) Available() error { return nil }
func (r *isolationRunner) Interactive(context.Context, io.Reader, io.Writer, io.Writer, ...string) error {
	return nil
}
func (r *isolationRunner) Run(_ context.Context, args ...string) (podman.Result, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	switch {
	case strings.HasPrefix(joined, "container ps"):
		if !strings.Contains(joined, "label="+podman.ManagedLabel+"=true") {
			return podman.Result{}, fmt.Errorf("missing V2 label filter")
		}
		return podman.Result{Stdout: `[{"Id":"v2-id","Names":"ca2-test","State":"running","Labels":{"io.containersagents.v2.managed":"true","io.containersagents.v2.environment-id":"v2-uuid"}}]`}, nil
	case strings.HasPrefix(joined, "container inspect"):
		return podman.Result{Stdout: `[{"Id":"v2-id","Name":"ca2-test","Config":{"Labels":{"io.containersagents.v2.managed":"true","io.containersagents.v2.environment-id":"v2-uuid"}},"State":{"Running":true,"Status":"running"},"HostConfig":{"Memory":1024}}]`}, nil
	case strings.HasPrefix(joined, "container stop"):
		return podman.Result{}, nil
	default:
		return podman.Result{}, fmt.Errorf("unexpected command %s", joined)
	}
}

func TestStopAllTouchesOnlyV2FilteredResources(t *testing.T) {
	root := t.TempDir()
	paths := state.Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"), State: filepath.Join(root, "state"), Cache: filepath.Join(root, "cache")}
	runner := &isolationRunner{}
	var output bytes.Buffer
	a := &Application{stdout: &output, stderr: &output, stdin: strings.NewReader(""), paths: paths, states: state.Store{Paths: paths}, envs: envstore.Store{Paths: paths}, profiles: profile.Store{Paths: paths}, runner: runner, podman: podman.Adapter{Runner: runner}}
	if err := a.environmentStop(context.Background(), []string{"--all"}); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, call := range runner.calls {
		joined += strings.Join(call, " ") + "\n"
	}
	if strings.Contains(joined, "prune") || strings.Contains(joined, "containers-agent-") {
		t.Fatalf("unsafe V1/global operation observed:\n%s", joined)
	}
	if !strings.Contains(joined, "container stop --time 10 v2-id") {
		t.Fatalf("V2 resource not stopped:\n%s", joined)
	}
}

func TestBuildContextLimitAppliesToEveryBuildPath(t *testing.T) {
	report := profile.ContextReport{Containerignore: true, SizeBytes: 2*resources.GiB + 1}
	if err := validateBuildContext("/tmp/context", report); err == nil || !strings.Contains(err.Error(), "above 2 GiB") {
		t.Fatalf("expected oversized context rejection, got %v", err)
	}
}
