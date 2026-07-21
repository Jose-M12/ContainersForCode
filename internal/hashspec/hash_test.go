package hashspec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryHonorsContainerignore(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".containerignore"), []byte(".env\n*.key\n"))
	mustWrite(t, filepath.Join(root, "source.txt"), []byte("one"))
	mustWrite(t, filepath.Join(root, ".env"), []byte("secret-one"))
	first, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, ".env"), []byte("secret-two"))
	second, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("ignored secret content changed the context hash")
	}
	mustWrite(t, filepath.Join(root, "source.txt"), []byte("two"))
	third, err := Directory(root)
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("included source change did not change context hash")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
