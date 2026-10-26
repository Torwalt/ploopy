package control

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestAnyRunningChecksLivePIDsAcrossRegistries(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	live := strconv.Itoa(os.Getpid()) + ".json"
	for _, name := range []string{"1073741824.json", "0.json", "-1.json", "note.json", strconv.Itoa(os.Getpid()) + ".adjust.json"} {
		if err := os.WriteFile(filepath.Join(first, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dirs := []string{filepath.Join(first, "missing"), first, second}
	if busy, err := AnyRunning(dirs); err != nil || busy {
		t.Fatalf("stale and unrelated files: running=%v, err=%v", busy, err)
	}
	// Malformed status must still prevent shutdown while its process is alive.
	path := filepath.Join(second, live)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if busy, err := AnyRunning(dirs); err != nil || !busy {
		t.Fatalf("live announcement in second registry: running=%v, err=%v", busy, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("checking runs changed the registry:", err)
	}
}

func TestAnyRunningReportsUnreadableRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AnyRunning([]string{path}); err == nil {
		t.Fatal("a registry that cannot be listed was treated as idle")
	}
}

func TestMachineDirsIncludesCustomAndFallbackRegistries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	fallback := filepath.Join(tmp, "ploopy-123")
	unrelated := filepath.Join(tmp, "ploopy-build")
	for _, dir := range []string{fallback, unrelated} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	caller := filepath.Join(tmp, "custom", "ploopy")
	dirs, err := MachineDirs(caller)
	if err != nil {
		t.Fatal(err)
	}
	found := make(map[string]bool)
	for _, dir := range dirs {
		found[dir] = true
	}
	if !found[caller] || !found[fallback] || found[unrelated] {
		t.Fatalf("wrong registry directories: %v", dirs)
	}
}

func TestActivityKeepsCommandVisibleWithoutAPlanStatus(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	release, err := RegisterActivity()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if busy, err := AnyRunning([]string{Dir()}); err != nil || !busy {
		t.Fatalf("activity without plan status: running=%v, err=%v", busy, err)
	}
	if lives, err := List(); err != nil || len(lives) != 0 {
		t.Fatalf("activity appeared as a selectable plan: %v, %v", lives, err)
	}
	release()
	if busy, err := AnyRunning([]string{Dir()}); err != nil || busy {
		t.Fatalf("released activity: running=%v, err=%v", busy, err)
	}
}
