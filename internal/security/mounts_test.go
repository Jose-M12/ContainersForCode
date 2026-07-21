package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"containersagents.dev/v2/internal/state"
)

func TestMountValidationAllowsNarrowProject(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "Customers", "Acme", "implementation")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	validator := MountValidator{Home: home, UID: -1}
	result, err := validator.Validate(project, false)
	if err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if result.Source != project {
		t.Fatalf("unexpected resolved path %q", result.Source)
	}
}

func TestMountValidationRejectsFullHome(t *testing.T) {
	home := t.TempDir()
	validator := MountValidator{Home: home, UID: -1}
	if _, err := validator.Validate(home, true); err == nil {
		t.Fatal("dangerous override must not permit the full home")
	}
}

func TestMountValidationRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	link := filepath.Join(home, "apparently-safe")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatal(err)
	}
	validator := MountValidator{Home: home, UID: -1}
	if _, err := validator.Validate(link, true); err == nil {
		t.Fatal("expected resolved /etc symlink rejection")
	}
}

func TestMountValidationRejectsV2State(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	stateDir := filepath.Join(home, ".local", "state", "containersagents-v2")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	validator := MountValidator{Home: home, Paths: state.Paths{State: stateDir}, UID: -1}
	if _, err := validator.Validate(stateDir, true); err == nil {
		t.Fatal("expected V2 state rejection")
	}
}

func TestMountValidationRejectsCommaDelimiter(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "project,with-option")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	validator := MountValidator{Home: home, UID: -1}
	if _, err := validator.Validate(project, false); err == nil || !strings.Contains(err.Error(), "comma delimiter") {
		t.Fatalf("expected comma delimiter rejection, got %v", err)
	}
}
