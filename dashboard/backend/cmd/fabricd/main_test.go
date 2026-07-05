package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveLoadBaselinesRoundTrip checks the file-level persistence helpers
// backing the baselines_path config key: saveBaselines writes atomically
// (tmp file + rename, 0644) and loadBaselines reads the same map back.
func TestSaveLoadBaselinesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baselines.json")

	want := map[string]float64{"150/br/rtt/1": 2.5, "151/br/rtt/1": 3.25}
	if err := saveBaselines(path, want); err != nil {
		t.Fatalf("saveBaselines: %v", err)
	}

	// The atomic-write temp file must not be left behind.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file left behind: err=%v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
	}

	got, err := loadBaselines(path)
	if err != nil {
		t.Fatalf("loadBaselines: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %s: got %v want %v", k, got[k], v)
		}
	}
}

// TestLoadBaselinesMissingFileIsNotError checks that a missing baselines
// file (the common case: first run, or baselines_path unset) is not an
// error -- fabricd should start cold, not fail.
func TestLoadBaselinesMissingFileIsNotError(t *testing.T) {
	dir := t.TempDir()
	m, err := loadBaselines(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("loadBaselines: %v", err)
	}
	if m != nil {
		t.Fatalf("m = %v, want nil for a missing file", m)
	}
}

// TestSaveBaselinesOverwritesExisting checks the rename-based atomic write
// correctly replaces a pre-existing file rather than erroring or appending.
func TestSaveBaselinesOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baselines.json")

	if err := saveBaselines(path, map[string]float64{"a": 1}); err != nil {
		t.Fatalf("saveBaselines (1st): %v", err)
	}
	if err := saveBaselines(path, map[string]float64{"a": 2, "b": 3}); err != nil {
		t.Fatalf("saveBaselines (2nd): %v", err)
	}

	got, err := loadBaselines(path)
	if err != nil {
		t.Fatalf("loadBaselines: %v", err)
	}
	if got["a"] != 2 || got["b"] != 3 || len(got) != 2 {
		t.Fatalf("got %v, want map[a:2 b:3]", got)
	}
}
