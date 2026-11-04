// Package web serves a DD-WRT-style admin interface.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fgbackman/router/internal/config"
	"github.com/fgbackman/router/internal/dhcp"
)

//go:embed templates/*.html
var templateFS embed.FS

// Status is what the daemon reports to the UI.
type Status struct {
	Version     string
	Started     time.Time
	Hostname    string
	WANIface    string
	WANAddrs    []netip.Prefix
	LANIface    string
	LANAddr     string
	Forwarding  string
	Leases      []dhcp.Lease
	DNSQueries  uint64
	DNSBlocked  uint64
	DNSLocal    uint64
	DNSForward  uint64
	DNSFailed   uint64
	BlockedSize int
	Upstreams   []netip.AddrPort
	PF          string
	Errors      []string
}

// Backend is implemented by the daemon.
type Backend interface {
	Config() *config.Config
	Apply(*config.Config) error
	Status() Status
}

type Server struct {
	Backend Backend
	Log     *log.Logger

	csrf string
	tmpl *template.Template
}

var funcs = template.FuncMap{
	"ago": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return time.Since(t).Round(time.Second).String()
	},
	"until": func(t time.Time) string {
		if t.IsZero() {
			return "static"
		}
		d := time.Until(t).Round(time.Minute)
		if d < 0 {
			return "expired"
		}
		return d.String()
	},
	"join": func(v any, sep string) string {
		switch xs := v.(type) {
		case []string:
			return strings.Join(xs, sep)
		case []netip.AddrPort:
			s := make([]string, len(xs))
			for i, x := range xs {
				s[i] = x.String()
			}
			return strings.Join(s, sep)
		case []netip.Prefix:
			s := make([]string, len(xs))
			for i, x := range xs {
				s[i] = x.String()
			}
			return strings.Join(s, sep)
		}
		return fmt.Sprint(v)
	},
	"hosts": func(hs []config.HostRecord) string {
		var b strings.Builder
		for _, h := range hs {
			fmt.Fprintf(&b, "%s %s\n", h.IP, h.Name)
		}
		return b.String()
	},
}

func (s *Server) init() error {
	if s.tmpl != nil {
		return nil
	}
	t, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return err
	}
	s.tmpl = t
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	s.csrf = hex.EncodeToString(tok)
	return nil
}

// Handler returns the full HTTP handler including auth.
func (s *Server) Handler() (http.Handler, error) {
	if err := s.init(); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page("status"))
	mux.HandleFunc("GET /setup", s.page("setup"))
	mux.HandleFunc("GET /dhcp", s.page("dhcp"))
	mux.HandleFunc("GET /firewall", s.page("firewall"))
	mux.HandleFunc("GET /dns", s.page("dns"))
	mux.HandleFunc("GET /pf", s.page("pf"))
	mux.HandleFunc("POST /setup", s.post("/setup", s.saveSetup))
	mux.HandleFunc("POST /firewall", s.post("/firewall", s.saveFirewall))
	mux.HandleFunc("POST /forward/add", s.post("/firewall", s.addForward))
	mux.HandleFunc("POST /forward/delete", s.post("/firewall", s.deleteForward))
	mux.HandleFunc("POST /static/add", s.post("/dhcp", s.addStatic))
	mux.HandleFunc("POST /static/delete", s.post("/dhcp", s.deleteStatic))
	mux.HandleFunc("POST /dns", s.post("/dns", s.saveDNS))
	return s.secure(mux), nil
}

// Serve runs the UI on addr until ctx ends.
func (s *Server) Serve(ctx context.Context, addr string) error {
	h, err := s.Handler()
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if s.Log != nil {
		s.Log.Printf("web: admin UI on http://%s/", addr)
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
		h.Set("Cache-Control", "no-store")

		cfg := s.Backend.Config()
		if cfg.Web.PasswordHash == "" {
			http.Error(w, "The admin UI is locked until a password is set. Run: sudo router passwd", http.StatusServiceUnavailable)
			return
		}
		user, pw, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Web.User)) != 1 || !config.CheckPassword(cfg.Web.PasswordHash, pw) {
			if ok {
				time.Sleep(time.Second) // slow down guessing
			}
			h.Set("WWW-Authenticate", `Basic realm="router", charset="UTF-8"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request refused", http.StatusForbidden)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(s.csrf)) != 1 {
				http.Error(w, "stale form; reload the page and try again", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type pageData struct {
	Page   string
	Cfg    *config.Config
	St     Status
	CSRF   string
	Msg    string
	Err    string
	Now    time.Time
	Static map[string]bool // MACs with static leases
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Backend.Config()
		d := pageData{
			Page: name, Cfg: cfg, St: s.Backend.Status(), CSRF: s.csrf,
			Msg: r.URL.Query().Get("msg"), Err: r.URL.Query().Get("err"), Now: time.Now(),
			Static: map[string]bool{},
		}
		for _, l := range cfg.DHCP.StaticLeases {
			if hw, err := net.ParseMAC(l.MAC); err == nil {
				d.Static[hw.String()] = true
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.tmpl.ExecuteTemplate(w, "layout.html", d); err != nil && s.Log != nil {
			s.Log.Printf("web: render %s: %v", name, err)
		}
	}
}

// post wraps a mutation: it edits a copy of the config and applies it.
func (s *Server) post(back string, edit func(*config.Config, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Backend.Config()
		err := edit(cfg, r)
		if err == nil {
			err = s.Backend.Apply(cfg)
		}
		q := url.Values{}
		if err != nil {
			q.Set("err", err.Error())
		} else {
			q.Set("msg", "Settings saved and applied.")
		}
		http.Redirect(w, r, back+"?"+q.Encode(), http.StatusSeeOther)
	}
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func list(s string) []string {
	return lines(strings.NewReplacer(",", "\n", " ", "\n").Replace(s))
}

func (s *Server) saveSetup(c *config.Config, r *http.Request) error {
	c.WAN.Interface = strings.TrimSpace(r.FormValue("wan"))
	c.LAN.Interface = strings.TrimSpace(r.FormValue("lan"))
	c.LAN.Address = strings.TrimSpace(r.FormValue("lanaddr"))
	c.LAN.ConfigureAddress = r.FormValue("configaddr") == "on"
	c.DHCP.Enabled = r.FormValue("dhcp") == "on"
	c.DHCP.RangeStart = strings.TrimSpace(r.FormValue("start"))
	c.DHCP.RangeEnd = strings.TrimSpace(r.FormValue("end"))
	lt, err := time.ParseDuration(strings.TrimSpace(r.FormValue("lease")))
	if err != nil {
		return fmt.Errorf("lease time: %v (use e.g. 12h)", err)
	}
	c.DHCP.LeaseTime.Duration = lt
	return nil
}

func (s *Server) saveFirewall(c *config.Config, r *http.Request) error {
	c.Firewall.BlockInbound = r.FormValue("block") == "on"
	c.Firewall.AllowPing = r.FormValue("ping") == "on"
	c.Firewall.OpenPorts = list(r.FormValue("open"))
	return nil
}

func (s *Server) addForward(c *config.Config, r *http.Request) error {
	ext, err1 := strconv.Atoi(r.FormValue("ext"))
	in, err2 := strconv.Atoi(r.FormValue("int"))
	if err1 != nil || err2 != nil {
		return errors.New("ports must be numbers")
	}
	c.Firewall.PortForwards = append(c.Firewall.PortForwards, config.PortForward{
		Name: strings.TrimSpace(r.FormValue("name")), Protocol: r.FormValue("proto"),
		ExternalPort: ext, InternalIP: strings.TrimSpace(r.FormValue("ip")), InternalPort: in,
	})
	return nil
}

func (s *Server) deleteForward(c *config.Config, r *http.Request) error {
	i, err := strconv.Atoi(r.FormValue("i"))
	if err != nil || i < 0 || i >= len(c.Firewall.PortForwards) {
		return errors.New("no such port forward")
	}
	c.Firewall.PortForwards = append(c.Firewall.PortForwards[:i], c.Firewall.PortForwards[i+1:]...)
	return nil
}

func (s *Server) addStatic(c *config.Config, r *http.Request) error {
	c.DHCP.StaticLeases = append(c.DHCP.StaticLeases, config.StaticLease{
		MAC: strings.TrimSpace(r.FormValue("mac")), IP: strings.TrimSpace(r.FormValue("ip")),
		Hostname: config.SanitizeHostname(r.FormValue("host")),
	})
	return nil
}

func (s *Server) deleteStatic(c *config.Config, r *http.Request) error {
	i, err := strconv.Atoi(r.FormValue("i"))
	if err != nil || i < 0 || i >= len(c.DHCP.StaticLeases) {
		return errors.New("no such static lease")
	}
	c.DHCP.StaticLeases = append(c.DHCP.StaticLeases[:i], c.DHCP.StaticLeases[i+1:]...)
	return nil
}

func (s *Server) saveDNS(c *config.Config, r *http.Request) error {
	c.DNS.Enabled = r.FormValue("enabled") == "on"
	c.DNS.Domain = strings.TrimSpace(r.FormValue("domain"))
	c.DNS.Upstreams = nil
	for _, u := range list(r.FormValue("upstreams")) {
		if a, err := netip.ParseAddr(u); err == nil {
			u = netip.AddrPortFrom(a, 53).String()
		}
		c.DNS.Upstreams = append(c.DNS.Upstreams, u)
	}
	c.DNS.BlockedDomains = list(r.FormValue("blocked"))
	c.DNS.Blocklists = lines(r.FormValue("lists"))
	c.DNS.Hosts = nil
	for i, l := range lines(r.FormValue("hosts")) {
		f := strings.Fields(l)
		if len(f) != 2 {
			return fmt.Errorf("hosts line %d: want \"IP name\"", i+1)
		}
		c.DNS.Hosts = append(c.DNS.Hosts, config.HostRecord{IP: f[0], Name: f[1]})
	}
	return nil
}
