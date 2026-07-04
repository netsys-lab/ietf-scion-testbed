// Package config loads the scion-linkd daemon configuration.
package config

import "github.com/BurntSushi/toml"

type Config struct {
	Listen       string `toml:"listen"`
	TopologyGlob string `toml:"topology_glob"`
}

func Load(path string) (Config, error) {
	c := Config{
		Listen:       ":30480",
		TopologyGlob: "/etc/scion/AS*/topology.json",
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}
