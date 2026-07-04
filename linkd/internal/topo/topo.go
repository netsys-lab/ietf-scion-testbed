// Package topo extracts border-router interfaces from SCION topology.json files.
package topo

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Interface struct {
	IfID     string
	Neighbor string
	LinkTo   string
	LocalIP  netip.Addr
}

type topoFile struct {
	BorderRouters map[string]struct {
		Interfaces map[string]struct {
			Underlay struct {
				Local string `json:"local"`
			} `json:"underlay"`
			IsdAs  string `json:"isd_as"`
			LinkTo string `json:"link_to"`
		} `json:"interfaces"`
	} `json:"border_routers"`
}

func Load(glob string) ([]Interface, error) {
	files, err := filepath.Glob(glob)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no topology files match %q", glob)
	}
	var out []Interface
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var t topoFile
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, br := range t.BorderRouters {
			for ifid, ic := range br.Interfaces {
				host := ic.Underlay.Local
				host = strings.TrimPrefix(host[:strings.LastIndex(host, ":")], "[")
				host = strings.TrimSuffix(host, "]")
				ip, err := netip.ParseAddr(host)
				if err != nil {
					return nil, fmt.Errorf("%s if %s: %w", f, ifid, err)
				}
				out = append(out, Interface{IfID: ifid, Neighbor: ic.IsdAs, LinkTo: ic.LinkTo, LocalIP: ip})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i].IfID)
		b, _ := strconv.Atoi(out[j].IfID)
		return a < b
	})
	return out, nil
}
