package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// endhostSDToml renders a portable endhost sciond config for AS asNum: bound
// to loopback, config_dir "." (certs from ./certs of the unpacked bundle),
// no [metrics] section, relative DB paths — self-contained kit. Attendee
// endhosts run upstream scionproto, which rejects the fork-only
// experimental_idint field in strict mode, so it is intentionally omitted
// (endhosts don't need ID-INT).
func endhostSDToml(asNum int) string {
	return fmt.Sprintf(`[general]
id = "sd1-%d"
config_dir = "."

[trust_db]
connection = "sd1-%d.trust.db"

[path_db]
connection = "sd1-%d.path.db"

[sd]
address = "127.0.0.1:30255"
query_interval = "30s"

[drkey_level2_db]
connection = "sd1-%d.drkey_level2.db"

[log.console]
level = "info"
`, asNum, asNum, asNum, asNum)
}

func bundleReadme(asNum int) string {
	return fmt.Sprintf("SCION endhost kit for AS 1-%d (IETF 126 attendee access)\n\n"+
		"Contents:\n"+
		"  topology.json            the AS topology\n"+
		"  sd.toml                  sciond config (loopback, config_dir \".\")\n"+
		"  certs/ISD1-B1-S1.trc     the ISD trust root (public)\n\n"+
		"Unpack into an empty directory and run sciond there with sd.toml.\n"+
		"Full setup (WireGuard, SCION tools, scitra, MTU caveats) lives on the\n"+
		"join page, or raw:\n\n"+
		"  /api/instructions/laptop.md\n\n"+
		"Keep tunnelled payloads under ~1200 bytes.\n", asNum)
}

func (s *server) handleJoinBundle(w http.ResponseWriter, r *http.Request) {
	if !s.join.Enabled {
		http.NotFound(w, r)
		return
	}
	asNum, err := strconv.Atoi(r.PathValue("as"))
	if err != nil {
		http.Error(w, "bad AS", http.StatusBadRequest)
		return
	}
	if !s.join.asAllowed(asNum) {
		http.NotFound(w, r)
		return
	}
	asDir := filepath.Join(s.join.ConfigDir, fmt.Sprintf("AS%d", asNum))
	topology, err := os.ReadFile(filepath.Join(asDir, "topology.json"))
	if err != nil {
		http.Error(w, "topology unavailable", http.StatusNotFound)
		return
	}
	trc, err := os.ReadFile(filepath.Join(asDir, "certs", "ISD1-B1-S1.trc"))
	if err != nil {
		http.Error(w, "trc unavailable", http.StatusNotFound)
		return
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{
		{"topology.json", topology},
		{"sd.toml", []byte(endhostSDToml(asNum))},
		{"certs/ISD1-B1-S1.trc", trc},
		{"README.txt", []byte(bundleReadme(asNum))},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), ModTime: time.Unix(0, 0)}); err != nil {
			http.Error(w, "archive error", http.StatusInternalServerError)
			return
		}
		if _, err := tw.Write(f.data); err != nil {
			http.Error(w, "archive error", http.StatusInternalServerError)
			return
		}
	}
	if err := tw.Close(); err != nil {
		http.Error(w, "archive error", http.StatusInternalServerError)
		return
	}
	if err := gz.Close(); err != nil {
		http.Error(w, "archive error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("scion-endhost-AS%d.tar.gz", asNum)))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}
