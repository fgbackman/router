package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/fgbackman/router/internal/config"
)

type fakeBackend struct{ cfg *config.Config }

func (f *fakeBackend) Config() *config.Config { return f.cfg.Clone() }
func (f *fakeBackend) Apply(c *config.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	f.cfg = c
	return nil
}
func (f *fakeBackend) Status() Status { return Status{Hostname: "mac", Version: "test"} }

func setup(t *testing.T) (*fakeBackend, http.Handler) {
	cfg := config.Default("en0", "en5")
	h, err := config.HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Web.PasswordHash = h
	fb := &fakeBackend{cfg: cfg}
	s := &Server{Backend: fb}
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return fb, handler
}

func do(h http.Handler, method, path string, form url.Values, auth bool) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if auth {
		r.SetBasicAuth("admin", "correct horse")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func csrf(t *testing.T, h http.Handler) string {
	w := do(h, "GET", "/firewall", nil, true)
	m := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no csrf token in page: %d", w.Code)
	}
	return m[1]
}

func TestPagesRender(t *testing.T) {
	_, h := setup(t)
	for _, p := range []string{"/", "/setup", "/dhcp", "/firewall", "/dns", "/pf"} {
		w := do(h, "GET", p, nil, true)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "</html>") {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	_, h := setup(t)
	if w := do(h, "GET", "/", nil, false); w.Code != 401 {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestLockedWithoutPassword(t *testing.T) {
	fb, h := setup(t)
	fb.cfg.Web.PasswordHash = ""
	if w := do(h, "GET", "/", nil, true); w.Code != 503 {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestAddAndDeleteForward(t *testing.T) {
	fb, h := setup(t)
	tok := csrf(t, h)
	form := url.Values{"csrf": {tok}, "name": {"web"}, "proto": {"tcp"}, "ext": {"8080"}, "ip": {"192.168.50.10"}, "int": {"80"}}
	w := do(h, "POST", "/forward/add", form, true)
	if w.Code != http.StatusSeeOther || strings.Contains(w.Header().Get("Location"), "err=") {
		t.Fatalf("add: %d %s", w.Code, w.Header().Get("Location"))
	}
	if len(fb.cfg.Firewall.PortForwards) != 1 {
		t.Fatal("forward not saved")
	}
	// Invalid input is reported, not applied.
	form.Set("ip", "8.8.8.8")
	w = do(h, "POST", "/forward/add", form, true)
	if !strings.Contains(w.Header().Get("Location"), "err=") || len(fb.cfg.Firewall.PortForwards) != 1 {
		t.Fatal("invalid forward accepted")
	}
	w = do(h, "POST", "/forward/delete", url.Values{"csrf": {tok}, "i": {"0"}}, true)
	if len(fb.cfg.Firewall.PortForwards) != 0 {
		t.Fatalf("delete failed: %s", w.Header().Get("Location"))
	}
}

func TestCSRFRejected(t *testing.T) {
	fb, h := setup(t)
	w := do(h, "POST", "/firewall", url.Values{"csrf": {"bogus"}}, true)
	if w.Code != 403 || !fb.cfg.Firewall.BlockInbound {
		t.Fatalf("csrf not enforced: %d", w.Code)
	}
}

func TestDNSForm(t *testing.T) {
	fb, h := setup(t)
	form := url.Values{"csrf": {csrf(t, h)}, "enabled": {"on"}, "domain": {"home"},
		"upstreams": {"1.1.1.1 9.9.9.9:53"}, "blocked": {"ads.example\ntracker.example"}, "hosts": {"192.168.50.20 nas"}}
	do(h, "POST", "/dns", form, true)
	c := fb.cfg.DNS
	if c.Domain != "home" || len(c.Upstreams) != 2 || c.Upstreams[0] != "1.1.1.1:53" || len(c.BlockedDomains) != 2 || c.Hosts[0].Name != "nas" {
		t.Fatalf("dns form: %+v", c)
	}
}
