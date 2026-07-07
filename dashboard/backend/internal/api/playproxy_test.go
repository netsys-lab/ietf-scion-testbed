package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
)

// echo backend: records path+auth for HTTP; echoes one message for WS.
func playBackend(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var lastPath string
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path + "|" + r.Header.Get("Authorization")
		if strings.Contains(r.Header.Get("Upgrade"), "websocket") {
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			mt, msg, _ := c.ReadMessage()
			c.WriteMessage(mt, msg)
			c.Close()
			return
		}
		w.Write([]byte("TTYD"))
	}))
	t.Cleanup(srv.Close)
	return srv, &lastPath
}

func newPlayServer(t *testing.T, target string) http.Handler {
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	jc := JoinConfig{PlayTargets: map[int]string{158: target}}
	return New(g, st, d, &fakeController{health: map[int]bool{}, shaping: map[string]*derive.Shaping{}}, nil, jc, nil, nil)
}

func TestPlayProxyStripsPrefixAndForwardsAuth(t *testing.T) {
	backend, lastPath := playBackend(t)
	h := newPlayServer(t, strings.TrimPrefix(backend.URL, "http://"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/play/158/token", nil)
	req.Header.Set("Authorization", "Basic c2Npb246eA==")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "TTYD" {
		t.Fatalf("got %d %q", rr.Code, rr.Body.String())
	}
	if *lastPath != "/token|Basic c2Npb246eA==" {
		t.Fatalf("backend saw %q", *lastPath)
	}
}

func TestPlayProxyRootRedirectsToSlash(t *testing.T) {
	backend, _ := playBackend(t)
	h := newPlayServer(t, strings.TrimPrefix(backend.URL, "http://"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/play/158", nil))
	if rr.Code != http.StatusMovedPermanently || rr.Header().Get("Location") != "/play/158/" {
		t.Fatalf("got %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestPlayProxyUnknownAS404(t *testing.T) {
	backend, _ := playBackend(t)
	h := newPlayServer(t, strings.TrimPrefix(backend.URL, "http://"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/play/150/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestPlayProxyWebSocket(t *testing.T) {
	backend, _ := playBackend(t)
	h := newPlayServer(t, strings.TrimPrefix(backend.URL, "http://"))
	front := httptest.NewServer(h)
	defer front.Close()
	u, _ := url.Parse(front.URL)
	u.Scheme = "ws"
	u.Path = "/play/158/ws"
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("ws dial through proxy: %v", err)
	}
	defer c.Close()
	c.WriteMessage(websocket.TextMessage, []byte("hi"))
	_, msg, err := c.ReadMessage()
	if err != nil || string(msg) != "hi" {
		t.Fatalf("ws echo through proxy: %q %v", msg, err)
	}
}
