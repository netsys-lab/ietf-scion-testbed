package wgpool

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writePool(t *testing.T, dir string, nslots int) string {
	t.Helper()
	p := struct {
		ServerPublicKey string `json:"server_public_key"`
		ListenPort      int    `json:"listen_port"`
		Slots           []Slot `json:"slots"`
	}{ServerPublicKey: "SPUB", ListenPort: 51820}
	for i := 0; i < nslots; i++ {
		n := i + 2
		p.Slots = append(p.Slots, Slot{N: n, IP: "10.20.5.2", PrivateKey: "priv", PublicKey: "pub"})
		p.Slots[i].IP = "10.20.5." + itoa(n)
	}
	b, _ := json.Marshal(p)
	path := filepath.Join(dir, "pool.json")
	os.WriteFile(path, b, 0o600)
	return path
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestClaimSequenceAndExhaustion(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(writePool(t, dir, 2), filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Claim(158)
	if err != nil || a.N != 2 {
		t.Fatalf("first claim: %+v %v", a, err)
	}
	b, err := s.Claim(159)
	if err != nil || b.N != 3 {
		t.Fatalf("second claim: %+v %v", b, err)
	}
	if _, err := s.Claim(160); !errors.Is(err, ErrExhausted) {
		t.Fatalf("want ErrExhausted, got %v", err)
	}
}

func TestClaimsPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	pool := writePool(t, dir, 2)
	state := filepath.Join(dir, "state.json")
	s, _ := Open(pool, state)
	s.Claim(158)
	s2, err := Open(pool, state)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Claim(158)
	if err != nil || got.N != 3 {
		t.Fatalf("reopened store must skip claimed slot 2: %+v %v", got, err)
	}
}

func TestBurnedSlotsSkipped(t *testing.T) {
	dir := t.TempDir()
	pool := writePool(t, dir, 2)
	state := filepath.Join(dir, "state.json")
	os.WriteFile(state, []byte(`{"claims":[],"burned":[2]}`), 0o600)
	s, _ := Open(pool, state)
	got, err := s.Claim(158)
	if err != nil || got.N != 3 {
		t.Fatalf("burned slot 2 must be skipped: %+v %v", got, err)
	}
	total, claimed, burned := s.Stats()
	if total != 2 || claimed != 1 || burned != 1 {
		t.Fatalf("stats = %d %d %d", total, claimed, burned)
	}
}

func TestConcurrentClaimsUnique(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(writePool(t, dir, 50), filepath.Join(dir, "state.json"))
	var mu sync.Mutex
	seen := map[int]bool{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sl, err := s.Claim(158)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			if seen[sl.N] {
				t.Errorf("slot %d claimed twice", sl.N)
			}
			seen[sl.N] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
}
