package manifest

import (
	"strings"
	"testing"
)

func TestDecodeStrictRejectsUnknownField(t *testing.T) {
	data := []byte(`{"apiVersion":"containersagents.dev/v2alpha1","kind":"Environment","metadata":{"name":"test-env","extra":true},"spec":{"profile":"debian-agent"}}`)
	_, err := DecodeStrict[Environment](data)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestEnvironmentRejectsSecretLikeEnvironmentVariable(t *testing.T) {
	environment := Environment{APIVersion: APIVersion, Kind: EnvironmentKind, Metadata: Metadata{Name: "test-env"}, Spec: EnvironmentSpec{Profile: "debian-agent", Environment: map[string]string{"API_TOKEN": "do-not-store"}}}
	err := ValidateEnvironment(environment)
	if err == nil || !strings.Contains(err.Error(), "spec.environment.API_TOKEN") {
		t.Fatalf("expected secret-like variable rejection, got %v", err)
	}
}

func TestApplyEnvironmentDefaults(t *testing.T) {
	profile := validTestProfile()
	environment := Environment{APIVersion: APIVersion, Kind: EnvironmentKind, Metadata: Metadata{Name: "test-env"}, Spec: EnvironmentSpec{Profile: "test-profile", Project: &ProjectSpec{Source: "/tmp/project"}}}
	ApplyEnvironmentDefaults(&environment, profile, BuiltinDefaults())
	if environment.Spec.Project.Target != "/workspace" || environment.Spec.Project.Mode != "read-write" {
		t.Fatalf("unexpected project defaults: %#v", environment.Spec.Project)
	}
	if environment.Spec.Home.Persistence != "per-environment" || environment.Spec.Concurrency.ProjectMode != "exclusive" {
		t.Fatalf("unexpected environment defaults: %#v", environment.Spec)
	}
}

func TestProfileBuildArgsRejectSecretNames(t *testing.T) {
	profile := validTestProfile()
	profile.Spec.Image.BuildArgs = map[string]string{"CUSTOMER_TOKEN": "value"}
	err := ValidateProfile(profile)
	if err == nil || !strings.Contains(err.Error(), "secret-like") {
		t.Fatalf("expected secret build argument rejection, got %v", err)
	}
}

func TestProfileRejectsUnsafePodmanValues(t *testing.T) {
	profile := validTestProfile()
	profile.Spec.Image.Repository = "--privileged"
	profile.Spec.Runtime.Home = "/"
	err := ValidateProfile(profile)
	if err == nil || !strings.Contains(err.Error(), "spec.image.repository") || !strings.Contains(err.Error(), "spec.runtime.home") {
		t.Fatalf("expected unsafe image and home rejection, got %v", err)
	}

	profile = validTestProfile()
	profile.Spec.Image.Mode = "existing"
	profile.Spec.Image.Repository = ""
	profile.Spec.Image.Containerfile = ""
	profile.Spec.Image.Context = ""
	profile.Spec.Image.Reference = " image"
	if err := ValidateProfile(profile); err == nil || !strings.Contains(err.Error(), "spec.image.reference") {
		t.Fatalf("expected unsafe image reference rejection, got %v", err)
	}
}

func TestEnvironmentRejectsCompoundOptionInjection(t *testing.T) {
	environment := Environment{APIVersion: APIVersion, Kind: EnvironmentKind, Metadata: Metadata{Name: "test-env"}, Spec: EnvironmentSpec{
		Profile: "debian-agent",
		Secrets: []SecretSpec{{Name: "source,target=/etc/passwd", Target: "/run/secrets/value", UID: -1}},
		Mounts:  []MountSpec{{Source: "/tmp/project,dst=/etc", Target: "/workspace", Mode: "read-only", Shared: true}},
	}}
	err := ValidateEnvironment(environment)
	if err == nil {
		t.Fatal("expected compound option rejection")
	}
	for _, expected := range []string{"spec.secrets[0].name", "uid and gid cannot be negative", "comma delimiters", "shared mount propagation"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("missing %q in %v", expected, err)
		}
	}
}

func validTestProfile() Profile {
	return Profile{APIVersion: APIVersion, Kind: ProfileKind, Metadata: Metadata{Name: "test-profile"}, Spec: ProfileSpec{
		Image:    ImageSpec{Mode: "build", Repository: "localhost/containersagents-v2/test", Containerfile: "Containerfile", Context: ".", PullPolicy: "newer"},
		Runtime:  RuntimeSpec{User: "agent", UID: 1000, GID: 1000, Home: "/home/agent", Workdir: "/workspace", Shell: []string{"/bin/sh"}, Keepalive: []string{"sleep", "infinity"}, IdentityMode: "managed-user"},
		Defaults: ProfileDefaults{SecurityClass: "development", ResourceClass: "balanced", RootFSPersistence: "persistent"},
	}}
}
