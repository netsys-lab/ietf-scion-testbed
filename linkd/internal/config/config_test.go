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

func TestMetadataDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(p, []byte("listen = \":1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.StaticinfoBase != "" {
		t.Fatalf("StaticinfoBase = %q, want empty (static info stays static by default; set staticinfo_base to re-enable the writer)", c.StaticinfoBase)
	}
	if c.StaticinfoOut != "" {
		t.Fatalf("StaticinfoOut = %q, want empty (derived)", c.StaticinfoOut)
	}
	if c.BaselineProfile != "/etc/scion/AS*/linkd-baseline.json" {
		t.Fatalf("BaselineProfile = %q", c.BaselineProfile)
	}
	if c.CSReloadUnit != "" {
		t.Fatalf("CSReloadUnit = %q, want empty", c.CSReloadUnit)
	}
}

func TestMetadataOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	body := "staticinfo_base = \"/x/base.json\"\nstaticinfo_out = \"/x/out.json\"\nbaseline_profile = \"/x/bl.json\"\ncs_reload_unit = \"scion-cs.service\"\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.StaticinfoBase != "/x/base.json" || c.StaticinfoOut != "/x/out.json" ||
		c.BaselineProfile != "/x/bl.json" || c.CSReloadUnit != "scion-cs.service" {
		t.Fatalf("overrides not applied: %+v", c)
	}
}
