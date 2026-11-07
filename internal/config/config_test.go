package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	if err := Default("en0", "en5").Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateErrors(t *testing.T) {
	c := Default("en0", "en0")
	c.DHCP.RangeEnd = "10.0.0.5"
	c.Firewall.PortForwards = []PortForward{{Protocol: "icmp", ExternalPort: 0, InternalIP: "8.8.8.8"}}
	c.DHCP.StaticLeases = []StaticLease{{MAC: "nope", IP: "192.168.50.5"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"must differ", "outside", "protocol", "ports", "on the LAN", "bad mac"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	c := Default("en0", "en7")
	c.DHCP.StaticLeases = []StaticLease{{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.50.20", Hostname: "nas"}}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.DHCP.LeaseTime != c.DHCP.LeaseTime || got.DHCP.StaticLeases[0].Hostname != "nas" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestPassword(t *testing.T) {
	h, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "hunter2") || CheckPassword(h, "hunter3") || CheckPassword("", "") {
		t.Fatal("password check wrong")
	}
}

func TestSanitizeHostname(t *testing.T) {
	cases := map[string]string{"Fred's iPhone": "fred-s-iphone", "laptop.local": "laptop", "--x_y--": "x-y", "💥": ""}
	for in, want := range cases {
		if got := SanitizeHostname(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestWebLANAddr(t *testing.T) {
	c := Default("en0", "en5")
	if got := c.WebLANAddr(); got != "192.168.50.1:8080" {
		t.Fatalf("got %q", got)
	}
	c.Web.Listen = "192.168.50.1:9000"
	if c.WebLANAddr() != "" {
		t.Fatal("listen already on LAN; no second listener")
	}
	c.Web.Listen = "127.0.0.1:8080"
	c.Web.AllowLAN = false
	if c.WebLANAddr() != "" {
		t.Fatal("AllowLAN off")
	}
}
