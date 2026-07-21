package podman

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

type stubResponse struct {
	result Result
	err    error
}

type stubRunner struct {
	responses        map[string][]stubResponse
	calls            [][]string
	interactiveCalls [][]string
	interactiveErr   error
}

func (r *stubRunner) Available() error { return nil }

func (r *stubRunner) Run(_ context.Context, args ...string) (Result, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	key := strings.Join(args, " ")
	responses := r.responses[key]
	if len(responses) == 0 {
		return Result{}, fmt.Errorf("unexpected Podman command: %s", key)
	}
	r.responses[key] = responses[1:]
	return responses[0].result, responses[0].err
}

func (r *stubRunner) Interactive(_ context.Context, _ io.Reader, _, _ io.Writer, args ...string) error {
	r.interactiveCalls = append(r.interactiveCalls, append([]string(nil), args...))
	return r.interactiveErr
}

func response(stdout string) []stubResponse {
	return []stubResponse{{result: Result{Stdout: stdout}}}
}

func commandError(stderr string, err error) error {
	return &CommandError{Operation: "test", Stderr: stderr, Err: err}
}

func exitOne(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit 1").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("could not produce exit status 1: %v", err)
	}
	return err
}

func TestExistenceChecksDistinguishMissingResources(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"image exists present":    response(""),
		"image exists absent":     {{err: commandError("", exitOne(t))}},
		"secret inspect present":  response("{}"),
		"secret inspect absent":   {{err: commandError("Error: no such secret", errors.New("inspect failed"))}},
		"network inspect present": response("[]"),
		"network inspect absent":  {{err: commandError("NO SUCH network", errors.New("inspect failed"))}},
	}}
	a := Adapter{Runner: runner}

	for _, test := range []struct {
		name string
		call func() (bool, error)
		want bool
	}{
		{"image present", func() (bool, error) { return a.ImageExists(context.Background(), "present") }, true},
		{"image absent", func() (bool, error) { return a.ImageExists(context.Background(), "absent") }, false},
		{"secret present", func() (bool, error) { return a.SecretExists(context.Background(), "present") }, true},
		{"secret absent", func() (bool, error) { return a.SecretExists(context.Background(), "absent") }, false},
		{"network present", func() (bool, error) { return a.NetworkExists(context.Background(), "present") }, true},
		{"network absent", func() (bool, error) { return a.NetworkExists(context.Background(), "absent") }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.call()
			if err != nil || got != test.want {
				t.Fatalf("got (%t, %v), want (%t, nil)", got, err, test.want)
			}
		})
	}
}

func TestExistenceChecksPreserveUnexpectedErrors(t *testing.T) {
	want := commandError("permission denied", errors.New("failed"))
	runner := &stubRunner{responses: map[string][]stubResponse{
		"image exists item":    {{err: want}},
		"secret inspect item":  {{err: want}},
		"network inspect item": {{err: want}},
	}}
	a := Adapter{Runner: runner}
	checks := []func() (bool, error){
		func() (bool, error) { return a.ImageExists(context.Background(), "item") },
		func() (bool, error) { return a.SecretExists(context.Background(), "item") },
		func() (bool, error) { return a.NetworkExists(context.Background(), "item") },
	}
	for _, check := range checks {
		if _, err := check(); !errors.Is(err, want) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestInspectNetworkParsesPodmanVariants(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"network inspect ca2-net": response(`[{"Name":"ca2-net","Id":"network-id","Labels":{"io.containersagents.v2.managed":"true","generation":2}}]`),
		"network inspect absent":  {{err: commandError("no such network", errors.New("missing"))}},
		"network inspect broken":  response(`not-json`),
		"network inspect many":    response(`[{"name":"one"},{"name":"two"}]`),
	}}
	a := Adapter{Runner: runner}

	network, exists, err := a.InspectNetwork(context.Background(), "ca2-net")
	if err != nil || !exists {
		t.Fatalf("inspect failed: exists=%t err=%v", exists, err)
	}
	want := Network{Name: "ca2-net", ID: "network-id", Labels: map[string]string{ManagedLabel: "true", "generation": "2"}}
	if !reflect.DeepEqual(network, want) {
		t.Fatalf("got %#v, want %#v", network, want)
	}
	if _, exists, err := a.InspectNetwork(context.Background(), "absent"); err != nil || exists {
		t.Fatalf("missing network result: exists=%t err=%v", exists, err)
	}
	if _, _, err := a.InspectNetwork(context.Background(), "broken"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
	if _, _, err := a.InspectNetwork(context.Background(), "many"); err == nil || !strings.Contains(err.Error(), "2 network inspect records") {
		t.Fatalf("expected record-count error, got %v", err)
	}
}

func TestInspectContainerParsesStateAndImageFallback(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"container inspect ca2-env": response(`[{"Id":"container-id","Name":"/ca2-env","Config":{"Labels":{"io.containersagents.v2.managed":"true"},"Image":"fallback-image"},"State":{"Running":true,"Status":"running","ExitCode":7},"HostConfig":{"Memory":536870912}}]`),
		"container inspect absent":  {{err: commandError("no such container", errors.New("missing"))}},
		"container inspect empty":   response(`[]`),
		"container inspect broken":  response(`{`),
	}}
	a := Adapter{Runner: runner}

	inspect, exists, err := a.InspectContainer(context.Background(), "ca2-env")
	if err != nil || !exists {
		t.Fatalf("inspect failed: exists=%t err=%v", exists, err)
	}
	if inspect.ID != "container-id" || inspect.Name != "ca2-env" || !inspect.Running || inspect.Status != "running" || inspect.ExitCode != 7 || inspect.Memory != 536870912 || inspect.Image != "fallback-image" || inspect.Labels[ManagedLabel] != "true" {
		t.Fatalf("unexpected inspect: %#v", inspect)
	}
	if _, exists, err := a.InspectContainer(context.Background(), "absent"); err != nil || exists {
		t.Fatalf("missing container result: exists=%t err=%v", exists, err)
	}
	if _, _, err := a.InspectContainer(context.Background(), "empty"); err == nil || !strings.Contains(err.Error(), "0 inspect records") {
		t.Fatalf("expected record-count error, got %v", err)
	}
	if _, _, err := a.InspectContainer(context.Background(), "broken"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestListManagedContainersHandlesPodmanJSONVariants(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"container ps --all --filter label=" + ManagedLabel + "=true --format json": response(`[
			{"Id":"first-id","Names":"/first","State":"running","Labels":{"one":"1"}},
			{"ID":"second-id","Names":["/second","alias"],"Status":"Exited (0)","Labels":{"two":"2"}}
		]`),
		"container ps --filter label=" + ManagedLabel + "=true --format json": response("  \n"),
	}}
	a := Adapter{Runner: runner}
	containers, err := a.ListManagedContainers(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := []Container{
		{ID: "first-id", Name: "first", State: "running", Labels: map[string]string{"one": "1"}},
		{ID: "second-id", Name: "second", State: "Exited (0)", Labels: map[string]string{"two": "2"}},
	}
	if !reflect.DeepEqual(containers, want) {
		t.Fatalf("got %#v, want %#v", containers, want)
	}
	empty, err := a.ListManagedContainers(context.Background(), false)
	if err != nil || len(empty) != 0 || empty == nil {
		t.Fatalf("empty list result: %#v, %v", empty, err)
	}
}

func TestRunningManagedMemorySumsOnlyRunningContainers(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"container ps --filter label=" + ManagedLabel + "=true --format json": response(`[{"Id":"one"},{"Id":"two"},{"Id":"gone"}]`),
		"container inspect one":  response(`[{"Id":"one","State":{"Running":true},"HostConfig":{"Memory":100}}]`),
		"container inspect two":  response(`[{"Id":"two","State":{"Running":false},"HostConfig":{"Memory":200}}]`),
		"container inspect gone": {{err: commandError("no such container", errors.New("gone"))}},
	}}
	memory, err := (Adapter{Runner: runner}).RunningManagedMemory(context.Background())
	if err != nil || memory != 100 {
		t.Fatalf("got memory=%d err=%v, want 100", memory, err)
	}
}

func TestListManagedImagesAndNetworks(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"image list --filter label=" + ManagedLabel + "=true --format json": response(`[
			{"Id":"image-one","Repository":"localhost/one","Tag":"abc","Labels":{"managed":"true"},"Size":123},
			{"ID":"image-two","Repository":"localhost/two","Tag":"def","Labels":{"managed":2},"Size":"456"}
		]`),
		"network ls --filter label=" + ManagedLabel + "=true --format json": response(`[
			{"Name":"ca2-one","Id":"network-one","Labels":{"managed":"true"}},
			{"Name":"ca2-two","ID":"network-two","Labels":{"managed":2}}
		]`),
	}}
	a := Adapter{Runner: runner}
	images, err := a.ListManagedImages(context.Background())
	if err != nil || len(images) != 2 || images[0].Size != 123 || images[1].Size != 456 || images[1].Labels["managed"] != "2" {
		t.Fatalf("unexpected images: %#v err=%v", images, err)
	}
	networks, err := a.ListManagedNetworks(context.Background())
	if err != nil || len(networks) != 2 || networks[0].ID != "network-one" || networks[1].ID != "network-two" || networks[1].Labels["managed"] != "2" {
		t.Fatalf("unexpected networks: %#v err=%v", networks, err)
	}
}

func TestAdapterForwardsMutatingCommandsExactly(t *testing.T) {
	runner := &stubRunner{responses: map[string][]stubResponse{
		"pull --policy missing image@sha256:digest": response(""),
		"build --tag image .":                       response(" image-id\n"),
		"container create --name ca2 image":         response(" container-id\n"),
		"container start ca2":                       response(""),
		"container stop --time 12 ca2":              response(""),
		"container rm ca2":                          response(""),
		"image rm image-id":                         response(""),
		"network create ca2-net":                    response(""),
		"network rm ca2-net":                        response(""),
		"container exec ca2 /bin/true":              response("ok\n"),
	}}
	a := Adapter{Runner: runner}
	ctx := context.Background()
	if err := a.Pull(ctx, "image@sha256:digest", "missing"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Build(ctx, []string{"build", "--tag", "image", "."}); err != nil || got != "image-id" {
		t.Fatalf("build got (%q, %v)", got, err)
	}
	if got, err := a.Create(ctx, []string{"container", "create", "--name", "ca2", "image"}); err != nil || got != "container-id" {
		t.Fatalf("create got (%q, %v)", got, err)
	}
	for _, err := range []error{
		a.Start(ctx, "ca2"),
		a.Stop(ctx, "ca2", 12),
		a.RemoveContainer(ctx, "ca2"),
		a.RemoveImage(ctx, "image-id"),
		a.CreateNetwork(ctx, []string{"network", "create", "ca2-net"}),
		a.RemoveNetwork(ctx, "ca2-net"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if result, err := a.Exec(ctx, "ca2", []string{"/bin/true"}); err != nil || result.Stdout != "ok\n" {
		t.Fatalf("exec got (%#v, %v)", result, err)
	}

	var stdout bytes.Buffer
	if err := a.ExecInteractive(ctx, strings.NewReader("input"), &stdout, &stdout, "ca2", []string{"sh"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RunInteractive(ctx, strings.NewReader("input"), &stdout, &stdout, []string{"run", "image"}); err != nil {
		t.Fatal(err)
	}
	wantInteractive := [][]string{{"container", "exec", "--interactive", "--tty", "ca2", "sh"}, {"run", "image"}}
	if !reflect.DeepEqual(runner.interactiveCalls, wantInteractive) {
		t.Fatalf("interactive calls got %#v, want %#v", runner.interactiveCalls, wantInteractive)
	}
}

func TestVerifyEnvironmentRequiresBothLabels(t *testing.T) {
	inspect := ContainerInspect{Name: "ca2-test", Labels: map[string]string{ManagedLabel: "true", EnvironmentIDLabel: "id-one"}}
	if err := VerifyEnvironment(inspect, "id-one"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyEnvironment(inspect, "id-two"); err == nil {
		t.Fatal("expected UUID ownership rejection")
	}
	inspect.Labels[ManagedLabel] = "false"
	if err := VerifyEnvironment(inspect, "id-one"); err == nil {
		t.Fatal("expected managed-label rejection")
	}
}
