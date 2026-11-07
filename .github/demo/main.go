// Command demo serves the admin API with sample data so CI can launch the
// Mac app against it and take a screenshot. Not part of the router.
package main

import (
	"log"
	"net/http"
	"net/netip"
	"time"

	"github.com/fgbackman/router/internal/config"
	"github.com/fgbackman/router/internal/dhcp"
	"github.com/fgbackman/router/internal/web"
)

type backend struct{ c *config.Config }

func (b *backend) Config() *config.Config       { return b.c.Clone() }
func (b *backend) Apply(c *config.Config) error { b.c = c.Clone(); return nil }
func (b *backend) Status() web.Status {
	ip := netip.MustParseAddr
	return web.Status{
		Version: "demo", Started: time.Now().Add(-26 * time.Hour), Hostname: "CCs-Mac-mini",
		WANIface: "en1", WANAddrs: []netip.Prefix{netip.MustParsePrefix("192.168.4.78/22")},
		LANIface: "en0", LANAddr: "192.168.50.1/24", Forwarding: "1",
		DNSQueries: 48211, DNSBlocked: 6034, DNSLocal: 912, DNSForward: 41265, BlockedSize: 79112,
		Upstreams: []netip.AddrPort{netip.MustParseAddrPort("192.168.4.1:53")},
		Leases: []dhcp.Lease{
			{MAC: "aa:bb:cc:dd:ee:ff", IP: ip("192.168.50.20"), Hostname: "nas", Static: true},
			{MAC: "3c:22:fb:10:aa:01", IP: ip("192.168.50.100"), Hostname: "iphone", Expires: time.Now().Add(9 * time.Hour)},
			{MAC: "f0:18:98:4c:12:9e", IP: ip("192.168.50.101"), Hostname: "living-room-tv", Expires: time.Now().Add(11 * time.Hour)},
		},
	}
}

func main() {
	c := config.Default("en1", "en0")
	c.Web.PasswordHash, _ = config.HashPassword("demo-password")
	c.DHCP.StaticLeases = []config.StaticLease{{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.50.20", Hostname: "nas"}}
	c.DNS.BlockedDomains = []string{"doubleclick.net", "ads.example.com"}
	c.DNS.Blocklists = []string{"https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"}
	c.Firewall.PortForwards = []config.PortForward{
		{Name: "Plex", Protocol: "tcp", ExternalPort: 32400, InternalIP: "192.168.50.20", InternalPort: 32400},
		{Name: "Game", Protocol: "both", ExternalPort: 3074, InternalIP: "192.168.50.101", InternalPort: 3074},
	}
	h, err := (&web.Server{Backend: &backend{c}}).Handler()
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", h))
}
