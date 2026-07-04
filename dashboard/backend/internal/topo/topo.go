// Package topo builds a topology Graph (ASes + inter-AS links) from a
// directory of SCION testbed configs: AS*/topology.json and as_list.yml.
package topo

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Endpoint describes one side of an inter-AS link.
type Endpoint struct {
	IA     string `json:"ia"` // "1-155"
	AS     int    `json:"as"` // 155
	IfID   string `json:"ifid"`
	IP     string `json:"ip"`      // underlay local host, "fd00:fade:9::155"
	LinkTo string `json:"link_to"` // parent|child|core|peer
}

// Link is one inter-AS connection, paired from the two ASes' topology.json
// interface entries. A is always the lower-numbered AS.
type Link struct {
	ID     string   `json:"id"`     // "151-155" (lower AS first)
	Type   string   `json:"type"`   // core|child|peer (A's perspective normalized)
	Subnet string   `json:"subnet"` // "fade:9"
	A      Endpoint `json:"a"`
	B      Endpoint `json:"b"`
}

// AS is one AS's summary metadata.
type AS struct {
	IA     string `json:"ia"`
	Num    int    `json:"num"`
	Core   bool   `json:"core"`
	MgmtIP string `json:"mgmt_ip"` // host of control_service addr
}

// Graph is the full topology: ASes sorted by Num, Links sorted by AS numbers.
type Graph struct {
	ASes  []AS   `json:"ases"`
	Links []Link `json:"links"`
}

// topology.json wire shapes (only the fields we need).
type topologyFile struct {
	IsdAs          string `json:"isd_as"`
	ControlService map[string]struct {
		Addr string `json:"addr"`
	} `json:"control_service"`
	BorderRouters map[string]struct {
		Interfaces map[string]struct {
			Underlay struct {
				Local  string `json:"local"`
				Remote string `json:"remote"`
			} `json:"underlay"`
			IsdAs  string `json:"isd_as"`
			LinkTo string `json:"link_to"`
		} `json:"interfaces"`
	} `json:"border_routers"`
}

// as_list.yml wire shape.
type asListFile struct {
	Core    []string `yaml:"Core"`
	NonCore []string `yaml:"Non-core"`
}

// ifaceRec is one border-router interface parsed out of a topology.json,
// flattened for pairing across AS files.
type ifaceRec struct {
	asNum      int
	ia         string
	ifID       string
	linkTo     string
	localAddr  string // raw "[fd00:fade:9::155]:50000", used as the pairing key
	remoteAddr string // raw remote addr, used to look up the counterpart
	localHost  string // bare "fd00:fade:9::155"
}

// Load reads every AS*/topology.json plus as_list.yml under configDir and
// builds the topology Graph.
func Load(configDir string) (Graph, error) {
	coreSet, err := loadCoreSet(configDir)
	if err != nil {
		return Graph{}, err
	}

	files, err := filepath.Glob(filepath.Join(configDir, "AS*", "topology.json"))
	if err != nil {
		return Graph{}, err
	}
	if len(files) == 0 {
		return Graph{}, fmt.Errorf("no topology.json files found under %s", configDir)
	}

	var ases []AS
	var recs []ifaceRec
	byLocalAddr := map[string]ifaceRec{}
	coreByNum := map[int]bool{}

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return Graph{}, err
		}
		var tf topologyFile
		if err := json.Unmarshal(raw, &tf); err != nil {
			return Graph{}, fmt.Errorf("%s: %w", f, err)
		}

		asNum, err := iaToNum(tf.IsdAs)
		if err != nil {
			return Graph{}, fmt.Errorf("%s: %w", f, err)
		}
		mgmtIP, err := firstControlServiceHost(tf.ControlService)
		if err != nil {
			return Graph{}, fmt.Errorf("%s: %w", f, err)
		}
		core := coreSet[tf.IsdAs]
		coreByNum[asNum] = core
		ases = append(ases, AS{IA: tf.IsdAs, Num: asNum, Core: core, MgmtIP: mgmtIP})

		for _, br := range tf.BorderRouters {
			for ifID, ic := range br.Interfaces {
				localHost, _, err := net.SplitHostPort(ic.Underlay.Local)
				if err != nil {
					return Graph{}, fmt.Errorf("%s if %s: %w", f, ifID, err)
				}
				rec := ifaceRec{
					asNum:      asNum,
					ia:         tf.IsdAs,
					ifID:       ifID,
					linkTo:     ic.LinkTo,
					localAddr:  ic.Underlay.Local,
					remoteAddr: ic.Underlay.Remote,
					localHost:  localHost,
				}
				recs = append(recs, rec)
				byLocalAddr[rec.localAddr] = rec
			}
		}
	}

	var links []Link
	for _, rec := range recs {
		counterpart, ok := byLocalAddr[rec.remoteAddr]
		if !ok {
			log.Printf("topo: dropping unpaired interface AS%d ifid=%s (remote %s not found)", rec.asNum, rec.ifID, rec.remoteAddr)
			continue
		}
		if rec.asNum >= counterpart.asNum {
			// Either a self-referential entry (ignored) or the other side
			// of a pair already emitted when we visited the lower-AS rec.
			continue
		}
		subnet, err := subnetOf(rec.localHost)
		if err != nil {
			return Graph{}, err
		}
		typ := "child"
		switch {
		case coreByNum[rec.asNum] && coreByNum[counterpart.asNum]:
			typ = "core"
		case rec.linkTo == "peer" || counterpart.linkTo == "peer":
			typ = "peer"
		}
		links = append(links, Link{
			ID:     fmt.Sprintf("%d-%d", rec.asNum, counterpart.asNum),
			Type:   typ,
			Subnet: subnet,
			A:      endpointFrom(rec),
			B:      endpointFrom(counterpart),
		})
	}

	sort.Slice(ases, func(i, j int) bool { return ases[i].Num < ases[j].Num })
	sort.Slice(links, func(i, j int) bool {
		if links[i].A.AS != links[j].A.AS {
			return links[i].A.AS < links[j].A.AS
		}
		return links[i].B.AS < links[j].B.AS
	})

	return Graph{ASes: ases, Links: links}, nil
}

func endpointFrom(r ifaceRec) Endpoint {
	return Endpoint{
		IA:     r.ia,
		AS:     r.asNum,
		IfID:   r.ifID,
		IP:     r.localHost,
		LinkTo: r.linkTo,
	}
}

// loadCoreSet parses as_list.yml into a set of core IA strings ("1-150").
func loadCoreSet(configDir string) (map[string]bool, error) {
	path := filepath.Join(configDir, "as_list.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var al asListFile
	if err := yaml.Unmarshal(raw, &al); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	core := make(map[string]bool, len(al.Core))
	for _, ia := range al.Core {
		core[ia] = true
	}
	return core, nil
}

// firstControlServiceHost returns the host part of the (deterministically
// chosen, lowest-keyed) control_service addr.
func firstControlServiceHost(cs map[string]struct {
	Addr string `json:"addr"`
}) (string, error) {
	if len(cs) == 0 {
		return "", fmt.Errorf("no control_service entries")
	}
	keys := make([]string, 0, len(cs))
	for k := range cs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	addr := cs[keys[0]].Addr
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("control_service addr %q: %w", addr, err)
	}
	return host, nil
}

// iaToNum extracts the AS number from an ISD-AS string like "1-155".
func iaToNum(ia string) (int, error) {
	parts := strings.Split(ia, "-")
	if len(parts) != 2 {
		return 0, fmt.Errorf("malformed isd_as %q", ia)
	}
	return strconv.Atoi(parts[1])
}

// subnetOf extracts the "fade:<hex>" subnet label from an underlay host like
// "fd00:fade:9::155".
func subnetOf(host string) (string, error) {
	const prefix = "fd00:fade:"
	if !strings.HasPrefix(host, prefix) {
		return "", fmt.Errorf("unexpected underlay host %q", host)
	}
	rest := host[len(prefix):]
	i := strings.Index(rest, "::")
	if i < 0 {
		return "", fmt.Errorf("unexpected underlay host %q", host)
	}
	return "fade:" + rest[:i], nil
}
