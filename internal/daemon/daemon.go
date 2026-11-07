// Package daemon wires the router together: system setup, pf, DHCP, DNS and
// the web UI, with live reload and clean teardown.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fgbackman/router/internal/config"
	"github.com/fgbackman/router/internal/dhcp"
	"github.com/fgbackman/router/internal/dns"
	"github.com/fgbackman/router/internal/pf"
	"github.com/fgbackman/router/internal/system"
	"github.com/fgbackman/router/internal/web"
)

var Version = "dev"

type Daemon struct {
	ConfigPath string
	Log        *log.Logger

	mu      sync.Mutex
	cfg     *config.Config
	state   *system.State
	pool    *dhcp.Pool
	dns     *dns.Server
	started time.Time

	errMu sync.Mutex
	errs  map[string]string

	reMu      sync.Mutex // serializes reconfigure (SIGHUP vs web UI)
	svcCancel context.CancelFunc
	svcWG     sync.WaitGroup
	rootCtx   context.Context
}

func New(configPath string, logger *log.Logger) *Daemon {
	return &Daemon{ConfigPath: configPath, Log: logger, errs: map[string]string{}, dns: &dns.Server{Log: logger}}
}

// Run brings the router up and blocks until ctx is cancelled or the process
// receives a stop signal, then restores the system. reload delivers SIGHUPs.
func (d *Daemon) Run(ctx context.Context, reload <-chan os.Signal) error {
	cfg, err := config.Load(d.ConfigPath)
	if err != nil {
		return err
	}
	d.rootCtx = ctx
	d.started = time.Now()
	if system.InternetSharingActive() {
		d.Log.Printf("warning: macOS Internet Sharing is on; turn it off in System Settings > General > Sharing, it conflicts with this router")
	}
	if err := d.up(cfg); err != nil {
		d.down()
		return err
	}
	d.startServices()
	d.Log.Printf("router %s up: %s (%s) -> %s", Version, cfg.LAN.Interface, cfg.LAN.Address, cfg.WAN.Interface)

	for {
		select {
		case <-ctx.Done():
			d.Log.Printf("shutting down")
			d.reMu.Lock()
			d.stopServices()
			d.down()
			d.reMu.Unlock()
			return nil
		case <-reload:
			d.Log.Printf("SIGHUP: reloading %s", d.ConfigPath)
			cfg, err := config.Load(d.ConfigPath)
			if err == nil {
				err = d.reconfigure(cfg)
			}
			if err != nil {
				d.Log.Printf("reload failed, keeping previous config: %v", err)
			}
		}
	}
}

// up applies system-level changes and records them for teardown.
func (d *Daemon) up(cfg *config.Config) error {
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return err
	}
	// A leftover state file means we crashed; undo that run first.
	if old, err := system.LoadState(cfg.StateDir); err == nil {
		d.Log.Printf("cleaning up after previous unclean exit")
		Teardown(cfg.StateDir, old, d.Log)
	}
	st := &system.State{}
	prev, err := system.Forwarding()
	if err != nil {
		return err
	}
	st.PrevForwarding = prev
	// Persist before each change so a crash midway is still recoverable.
	save := func() error { return st.Save(cfg.StateDir) }
	if err := save(); err != nil {
		return err
	}
	d.mu.Lock()
	d.cfg, d.state = cfg, st
	d.mu.Unlock()

	if err := system.SetForwarding(true); err != nil {
		return err
	}
	if cfg.LAN.ConfigureAddress {
		p, _ := cfg.LANPrefix()
		added, err := system.AddAlias(cfg.LAN.Interface, p)
		if err != nil {
			return fmt.Errorf("assign %s to %s: %w", p, cfg.LAN.Interface, err)
		}
		if added {
			st.AliasIface, st.AliasAddr = cfg.LAN.Interface, p.Addr()
			if err := save(); err != nil {
				return err
			}
		}
	}
	if !system.HasAddress(cfg.LAN.Interface, cfg.RouterIP()) {
		return fmt.Errorf("%s does not have address %s; set it in System Settings or enable lan.configureAddress", cfg.LAN.Interface, cfg.RouterIP())
	}
	rules, err := pf.Render(cfg)
	if err != nil {
		return err
	}
	tok, err := pf.Load(cfg.StateDir, rules)
	if err != nil {
		return err
	}
	st.PFToken = tok
	return save()
}

func (d *Daemon) down() {
	d.mu.Lock()
	cfg, st := d.cfg, d.state
	d.mu.Unlock()
	if cfg == nil || st == nil {
		return
	}
	Teardown(cfg.StateDir, st, d.Log)
}

// Teardown reverses what up recorded in st. It is also used by `router down`.
func Teardown(stateDir string, st *system.State, logger *log.Logger) {
	logf := func(format string, a ...any) {
		if logger != nil {
			logger.Printf(format, a...)
		}
	}
	if err := pf.Flush(st.PFToken); err != nil {
		logf("pf flush: %v", err)
	}
	if st.AliasIface != "" && st.AliasAddr.IsValid() {
		if err := system.RemoveAlias(st.AliasIface, st.AliasAddr); err != nil {
			logf("remove address: %v", err)
		}
	}
	if st.PrevForwarding != "" {
		if err := system.SetForwarding(st.PrevForwarding == "1"); err != nil {
			logf("restore forwarding: %v", err)
		}
	}
	if err := system.RemoveState(stateDir); err != nil {
		logf("remove state: %v", err)
	}
}

// reconfigure applies a new config, redoing system setup only when the
// interfaces or LAN address changed.
func (d *Daemon) reconfigure(cfg *config.Config) error {
	d.reMu.Lock()
	defer d.reMu.Unlock()
	d.mu.Lock()
	old := d.cfg
	d.mu.Unlock()
	rules, err := pf.Render(cfg)
	if err != nil {
		return err
	}
	d.stopServices()
	sameNet := old.WAN == cfg.WAN && old.LAN == cfg.LAN && old.StateDir == cfg.StateDir
	if sameNet {
		if err := pf.Reload(cfg.StateDir, rules); err != nil {
			d.startServices() // keep running on the old config
			return err
		}
		d.mu.Lock()
		d.cfg = cfg
		d.mu.Unlock()
	} else {
		d.down()
		if err := d.up(cfg); err != nil {
			d.Log.Printf("new network config failed (%v); restoring previous", err)
			d.down()
			if err2 := d.up(old); err2 != nil {
				return errors.Join(err, fmt.Errorf("restore failed: %w", err2))
			}
			d.startServices()
			return err
		}
	}
	d.startServices()
	return nil
}

func (d *Daemon) setErr(component string, err error) {
	d.errMu.Lock()
	defer d.errMu.Unlock()
	if err == nil {
		delete(d.errs, component)
	} else {
		d.errs[component] = err.Error()
		d.Log.Printf("%s: %v", component, err)
	}
}

// startServices launches DHCP, DNS and the web UI for the current config.
func (d *Daemon) startServices() {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	ctx, cancel := context.WithCancel(d.rootCtx)
	d.svcCancel = cancel
	lan, _ := cfg.LANPrefix()
	routerIP := cfg.RouterIP()

	run := func(name string, f func(context.Context) error) {
		d.svcWG.Add(1)
		go func() {
			defer d.svcWG.Done()
			for {
				err := f(ctx)
				d.setErr(name, err)
				if ctx.Err() != nil || err == nil {
					return
				}
				// Retry: e.g. the LAN cable was unplugged or the port was busy.
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
			}
		}()
	}

	// DNS servers handed to DHCP clients.
	var clientDNS []netip.Addr
	if cfg.DNS.Enabled {
		clientDNS = []netip.Addr{routerIP}
	} else {
		for _, u := range d.upstreams(cfg) {
			clientDNS = append(clientDNS, u.Addr())
		}
	}

	pool, err := dhcp.NewPool(cfg, filepath.Join(cfg.StateDir, "leases.json"))
	if err != nil {
		d.setErr("dhcp", err)
	}
	d.mu.Lock()
	d.pool = pool
	d.mu.Unlock()

	if cfg.DHCP.Enabled && pool != nil {
		srv := &dhcp.Server{
			Pool: pool, Interface: cfg.LAN.Interface, ServerIP: routerIP, Subnet: lan.Masked(),
			DNS: clientDNS, Domain: cfg.DNS.Domain, LeaseTime: cfg.DHCP.LeaseTime.Duration, Log: d.Log,
		}
		run("dhcp", srv.Serve)
	} else {
		d.setErr("dhcp", nil)
	}

	if cfg.DNS.Enabled {
		s := d.dns
		s.Listen, s.Domain, s.Subnet = routerIP, cfg.DNS.Domain, lan.Masked()
		if pool != nil {
			s.Leases = pool
		}
		hosts := map[string]netip.Addr{}
		for _, h := range cfg.DNS.Hosts {
			if a, err := netip.ParseAddr(h.IP); err == nil {
				hosts[h.Name] = a
			}
		}
		manual, _ := dns.LoadSources(ctx, nil, cfg.DNS.BlockedDomains, "")
		s.Update(d.upstreams(cfg), hosts, manual)
		run("dns", s.Serve)

		// Blocklist downloads and upstream refresh happen in the background.
		d.svcWG.Add(1)
		go func() {
			defer d.svcWG.Done()
			cacheDir := filepath.Join(cfg.StateDir, "blocklists")
			loadLists := func() {
				if len(cfg.DNS.Blocklists) == 0 {
					return
				}
				bl, errs := dns.LoadSources(ctx, cfg.DNS.Blocklists, cfg.DNS.BlockedDomains, cacheDir)
				if ctx.Err() != nil {
					return
				}
				d.setErr("blocklists", errors.Join(errs...))
				manual = bl
				s.Update(d.upstreams(cfg), hosts, bl)
				d.Log.Printf("dns: %d domains blocked", bl.Len())
			}
			loadLists()
			upTick := time.NewTicker(time.Minute)
			listTick := time.NewTicker(24 * time.Hour)
			defer upTick.Stop()
			defer listTick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-upTick.C:
					s.Update(d.upstreams(cfg), hosts, manual)
				case <-listTick.C:
					loadLists()
				}
			}
		}()
	} else {
		d.setErr("dns", nil)
	}

	if cfg.Web.Enabled {
		ws := &web.Server{Backend: d, Log: d.Log}
		if _, err := ws.Handler(); err != nil { // initialise once before serving twice
			d.setErr("web", err)
		} else {
			run("web", func(ctx context.Context) error { return ws.Serve(ctx, cfg.Web.Listen) })
			if lan := cfg.WebLANAddr(); lan != "" {
				run("web-lan", func(ctx context.Context) error { return ws.Serve(ctx, lan) })
			} else {
				d.setErr("web-lan", nil)
			}
		}
	} else {
		d.setErr("web", nil)
	}
}

func (d *Daemon) stopServices() {
	if d.svcCancel != nil {
		d.svcCancel()
	}
	d.svcWG.Wait()
}

func (d *Daemon) upstreams(cfg *config.Config) []netip.AddrPort {
	var out []netip.AddrPort
	for _, u := range cfg.DNS.Upstreams {
		if a, err := netip.ParseAddrPort(u); err == nil {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		out = dns.SystemUpstreams("/etc/resolv.conf", cfg.RouterIP())
	}
	if len(out) == 0 {
		out = []netip.AddrPort{netip.MustParseAddrPort("1.1.1.1:53"), netip.MustParseAddrPort("9.9.9.9:53")}
	}
	return out
}

// --- web.Backend ---

func (d *Daemon) Config() *config.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg.Clone()
}

// Apply validates, saves and applies a config edited in the web UI. The
// reconfigure runs asynchronously because it restarts the web server that
// is handling the request.
func (d *Daemon) Apply(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	rules, err := pf.Render(cfg)
	if err != nil {
		return err
	}
	if err := pf.Check(rules); err != nil {
		return err
	}
	if err := cfg.Save(d.ConfigPath); err != nil {
		return err
	}
	go func() {
		time.Sleep(200 * time.Millisecond) // let the redirect reach the browser
		if err := d.reconfigure(cfg); err != nil {
			d.setErr("apply", err)
		} else {
			d.setErr("apply", nil)
		}
	}()
	return nil
}

func (d *Daemon) Status() web.Status {
	d.mu.Lock()
	cfg, pool := d.cfg, d.pool
	d.mu.Unlock()
	host, _ := os.Hostname()
	fw, _ := system.Forwarding()
	st := web.Status{
		Version: Version, Started: d.started, Hostname: host,
		WANIface: cfg.WAN.Interface, WANAddrs: system.IPv4Addrs(cfg.WAN.Interface),
		LANIface: cfg.LAN.Interface, LANAddr: cfg.LAN.Address, Forwarding: fw,
		DNSQueries: d.dns.Queries.Load(), DNSBlocked: d.dns.Blocked.Load(), DNSLocal: d.dns.Local.Load(),
		DNSForward: d.dns.Forwarded.Load(), DNSFailed: d.dns.Failed.Load(),
		BlockedSize: d.dns.BlockedCount(), Upstreams: d.dns.Upstreams(), PF: pf.Status(),
	}
	if pool != nil {
		st.Leases = pool.Active()
	}
	d.errMu.Lock()
	for k, v := range d.errs {
		st.Errors = append(st.Errors, k+": "+v)
	}
	d.errMu.Unlock()
	return st
}
