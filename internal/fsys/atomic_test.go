package fsys_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.toml")

	data := []byte("hello = true\n")
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, path, data, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading result: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content = %q, want %q", got, data)
	}
}

func TestWriteFileAtomic_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.toml")

	// Write initial content.
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Overwrite atomically.
	data := []byte("new")
	if err := fsys.WriteFileAtomic(fsys.OSFS{}, path, data, 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}

	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "test.toml" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestWriteFileAtomic_SweepsDeadPIDOrphans(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")

	deadPID := unusedPID(t)
	deadOrphan := fmt.Sprintf("%s.tmp.%d.di258fvm6o6j", target, deadPID)
	if err := os.WriteFile(deadOrphan, nil, 0o644); err != nil {
		t.Fatalf("seed dead orphan: %v", err)
	}

	livePID := os.Getpid()
	liveOrphan := fmt.Sprintf("%s.tmp.%d.di258dn070o5", target, livePID)
	if err := os.WriteFile(liveOrphan, nil, 0o644); err != nil {
		t.Fatalf("seed live orphan: %v", err)
	}

	siblingOrphan := filepath.Join(dir, "other.json.tmp."+fmt.Sprint(deadPID)+".di258gflei66")
	if err := os.WriteFile(siblingOrphan, nil, 0o644); err != nil {
		t.Fatalf("seed sibling orphan: %v", err)
	}

	if err := fsys.WriteFileAtomic(fsys.OSFS{}, target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	if _, err := os.Stat(deadOrphan); !os.IsNotExist(err) {
		t.Errorf("dead-PID orphan still present: stat err = %v", err)
	}
	if _, err := os.Stat(liveOrphan); err != nil {
		t.Errorf("live-PID orphan unexpectedly removed: stat err = %v", err)
	}
	if _, err := os.Stat(siblingOrphan); err != nil {
		t.Errorf("unrelated-basename orphan unexpectedly swept: stat err = %v", err)
	}
}

func TestWriteFileAtomic_PreservesUnparseablePeers(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.yaml")

	noise := []string{
		target + ".tmp.notapid.suffix",
		target + ".tmp.",
		target + ".tmp",
		target + ".bak",
		filepath.Join(dir, "config.yaml.swp"),
	}
	for _, name := range noise {
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	if err := fsys.WriteFileAtomic(fsys.OSFS{}, target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	for _, name := range noise {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("preserved peer %s was removed: %v", name, err)
		}
	}
}

// unusedPID returns a PID large enough to be reasonably absent on the host.
// On Linux the default pid_max is 4194304; 4_000_001 leaves room without
// straddling that ceiling. The test fails closed if /proc says the PID is
// alive (extremely unlikely under test).
func unusedPID(t *testing.T) int {
	t.Helper()
	for _, candidate := range []int{4_000_001, 3_999_999, 999_999} {
		if _, err := os.Stat(fmt.Sprintf("/proc/%d", candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
	t.Skip("no unused PID candidate found on this host")
	return 0
}

