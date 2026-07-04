package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(""), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":30480" || c.TopologyGlob != "/etc/scion/AS*/topology.json" {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("listen = \"10.20.3.155:30480\"\ntopology_glob = \"/tmp/x/*.json\"\n"), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "10.20.3.155:30480" || c.TopologyGlob != "/tmp/x/*.json" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("/nonexistent/config.toml"); err == nil {
		t.Fatal("want error")
	}
}
