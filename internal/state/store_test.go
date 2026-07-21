package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterUsesUUIDAndPrivatePermissions(t *testing.T) {
	root := t.TempDir()
	paths := Paths{Config: filepath.Join(root, "config"), Data: filepath.Join(root, "data"), State: filepath.Join(root, "state"), Cache: filepath.Join(root, "cache")}
	store := Store{Paths: paths}
	created, err := store.Register("test-env")
	if err != nil {
		t.Fatal(err)
	}
	if !uuidPattern.MatchString(created.ID) {
		t.Fatalf("invalid UUID %q", created.ID)
	}
	info, err := os.Stat(paths.EnvironmentStateFile(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("state mode is %o", info.Mode().Perm())
	}
	homeInfo, err := os.Stat(paths.EnvironmentData(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	if homeInfo.Mode().Perm() != 0700 {
		t.Fatalf("data mode is %o", homeInfo.Mode().Perm())
	}
}
