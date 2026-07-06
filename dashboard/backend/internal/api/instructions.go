package api

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// instrNameRe restricts an instruction name to one flat .md file — no path
// separators, no "..": the name can never escape InstructionsDir.
var instrNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+\.md$`)

type instrEntry struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func titleOf(md []byte, fallback string) string {
	for _, line := range strings.Split(string(md), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return fallback
}

func (s *server) handleInstructionsList(w http.ResponseWriter, r *http.Request) {
	out := []instrEntry{}
	if s.join.InstructionsDir != "" {
		if entries, err := os.ReadDir(s.join.InstructionsDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				title := e.Name()
				if md, err := os.ReadFile(filepath.Join(s.join.InstructionsDir, e.Name())); err == nil {
					title = titleOf(md, e.Name())
				}
				out = append(out, instrEntry{Name: e.Name(), Title: title})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleInstruction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !instrNameRe.MatchString(name) {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	if s.join.InstructionsDir == "" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.join.InstructionsDir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
