// Package wgpool hands out preconfigured WireGuard conf slots from a
// generator-produced pool file. Claim state is persisted atomically
// (temp+rename, the fabricd saveBaselines pattern) so a fabricd restart never
// reissues a claimed slot. Burned slots (leaked/revoked keys, hand-edited
// into the state file per the runbook) are never reissued either.
package wgpool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Slot struct {
	N          int    `json:"n"`
	IP         string `json:"ip"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

var ErrExhausted = errors.New("wg conf pool exhausted")

type poolFile struct {
	ServerPublicKey string `json:"server_public_key"`
	ListenPort      int    `json:"listen_port"`
	Slots           []Slot `json:"slots"`
}

type claim struct {
	N  int       `json:"n"`
	AS int       `json:"as"`
	At time.Time `json:"at"`
}

type stateFile struct {
	Claims []claim `json:"claims"`
	Burned []int   `json:"burned"`
}

type Store struct {
	mu        sync.Mutex
	pool      poolFile
	statePath string
	state     stateFile
	taken     map[int]bool // claimed or burned
}

func Open(poolPath, statePath string) (*Store, error) {
	b, err := os.ReadFile(poolPath)
	if err != nil {
		return nil, fmt.Errorf("wgpool: read pool: %w", err)
	}
	s := &Store{statePath: statePath, taken: map[int]bool{}}
	if err := json.Unmarshal(b, &s.pool); err != nil {
		return nil, fmt.Errorf("wgpool: parse pool: %w", err)
	}
	if sb, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(sb, &s.state); err != nil {
			return nil, fmt.Errorf("wgpool: parse state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("wgpool: read state: %w", err)
	}
	for _, c := range s.state.Claims {
		s.taken[c.N] = true
	}
	for _, n := range s.state.Burned {
		s.taken[n] = true
	}
	return s, nil
}

func (s *Store) Claim(as int) (Slot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sl := range s.pool.Slots {
		if s.taken[sl.N] {
			continue
		}
		s.taken[sl.N] = true
		s.state.Claims = append(s.state.Claims, claim{N: sl.N, AS: as, At: time.Now().UTC()})
		if err := s.persistLocked(); err != nil {
			return Slot{}, err
		}
		return sl, nil
	}
	return Slot{}, ErrExhausted
}

func (s *Store) Stats() (total, claimed, burned int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pool.Slots), len(s.state.Claims), len(s.state.Burned)
}

func (s *Store) ServerPublicKey() string { return s.pool.ServerPublicKey }

func (s *Store) persistLocked() error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		return err
	}
	_ = filepath.Dir(s.statePath) // no fsync of dir: acceptable for booth state
	return nil
}
