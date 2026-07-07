// Package config loads the scion-linkd daemon configuration.
package config

import "github.com/BurntSushi/toml"

type Config struct {
	Listen       string `toml:"listen"`
	TopologyGlob string `toml:"topology_glob"`
	// StaticinfoBase is empty by default: static info stays static. The
	// story values gen_staticinfo.py bakes into staticInfoConfig.base.json
	// are the permanent advertisements — shaping changes only measured
	// state (BFD RTT, ID-INT), never the beacon metadata. Setting this key
	// re-enables linkd's staticinfo writer + CS SIGHUP reload machinery
	// (kept in the codebase as an escape hatch, not part of the default
	// deploy).
	StaticinfoBase  string `toml:"staticinfo_base"`
	StaticinfoOut   string `toml:"staticinfo_out"`
	BaselineProfile string `toml:"baseline_profile"`
	CSReloadUnit    string `toml:"cs_reload_unit"`
}

func Load(path string) (Config, error) {
	c := Config{
		Listen:          ":30480",
		TopologyGlob:    "/etc/scion/AS*/topology.json",
		BaselineProfile: "/etc/scion/AS*/linkd-baseline.json",
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}
