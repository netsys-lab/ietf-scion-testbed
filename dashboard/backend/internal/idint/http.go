package idint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// httpProber implements Prober against the real idint-probed sidecars: HTTP
// on each AS container's management IP. Every call (paths for src->dst,
// probe of src's prober against dst's reflector) is dialed from the src
// AS's own prober, mirroring where the SCION client actually lives.
type httpProber struct {
	proberPort    int
	reflectorPort int
	c             *http.Client
	mgmtByNum     map[int]string
	iaByNum       map[int]string
}

// NewHTTPProber builds a Prober that talks to the idint-probed sidecar on
// each AS's management IP (from g.ASes[i].MgmtIP), never a synthesized
// address.
func NewHTTPProber(g topo.Graph, proberPort, reflectorPort int, c *http.Client) Prober {
	p := &httpProber{
		proberPort:    proberPort,
		reflectorPort: reflectorPort,
		c:             c,
		mgmtByNum:     make(map[int]string, len(g.ASes)),
		iaByNum:       make(map[int]string, len(g.ASes)),
	}
	for _, as := range g.ASes {
		p.mgmtByNum[as.Num] = as.MgmtIP
		p.iaByNum[as.Num] = as.IA
	}
	return p
}

// Paths calls GET /api/v1/paths?dst=<dstIA> on src's prober.
func (p *httpProber) Paths(ctx context.Context, src, dst int) (*PathsResponse, error) {
	u := fmt.Sprintf("http://%s/api/v1/paths?dst=%s",
		net.JoinHostPort(p.mgmtByNum[src], strconv.Itoa(p.proberPort)),
		url.QueryEscape(p.iaByNum[dst]))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var resp PathsResponse
	if err := p.do(req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Probe calls POST /api/v1/probe on src's prober, with a "remote" pointing
// at dst's ID-INT reflector.
func (p *httpProber) Probe(ctx context.Context, src, dst int, fingerprint string) (*ProbeResult, error) {
	remote := fmt.Sprintf("%s,%s", p.iaByNum[dst],
		net.JoinHostPort(p.mgmtByNum[dst], strconv.Itoa(p.reflectorPort)))
	body, err := json.Marshal(map[string]string{"remote": remote, "fingerprint": fingerprint})
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("http://%s/api/v1/probe", net.JoinHostPort(p.mgmtByNum[src], strconv.Itoa(p.proberPort)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var res ProbeResult
	if err := p.do(req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// do executes req and decodes a 200 JSON body into out. A non-200 response
// is expected to carry {"error":"..."}; its message is folded into the
// returned error.
func (p *httpProber) do(req *http.Request, out any) error {
	resp, err := p.c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("prober %s: %s", resp.Status, e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
