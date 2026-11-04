package pf

import (
	"strings"
	"testing"

	"github.com/fgbackman/router/internal/config"
)

func TestRender(t *testing.T) {
	c := config.Default("en0", "en5")
	c.Firewall.AllowPing = true
	c.Firewall.OpenPorts = []string{"tcp/22"}
	c.Firewall.PortForwards = []config.PortForward{
		{Name: "web", Protocol: "tcp", ExternalPort: 8080, InternalIP: "192.168.50.10", InternalPort: 80},
		{Name: "game", Protocol: "both", ExternalPort: 3074, InternalIP: "192.168.50.11", InternalPort: 3074},
	}
	got, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`lan_net = "192.168.50.0/24"`,
		"nat on $wan_if inet from $lan_net to any -> ($wan_if)",
		"rdr pass on $wan_if inet proto tcp from any to ($wan_if) port 8080 -> 192.168.50.10 port 80 # web",
		"rdr pass on $wan_if inet proto { tcp udp } from any to ($wan_if) port 3074 -> 192.168.50.11 port 3074 # game",
		"block in log on $wan_if inet all",
		"icmp-type echoreq",
		"to ($wan_if) port 22 keep state",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// Translation rules must precede filter rules or pfctl rejects the file.
	if strings.Index(got, "rdr ") > strings.Index(got, "block ") {
		t.Error("rdr after filter rules")
	}
}

func TestRenderNoBlock(t *testing.T) {
	c := config.Default("en0", "en5")
	c.Firewall.BlockInbound = false
	got, _ := Render(c)
	if strings.Contains(got, "block") {
		t.Error("unexpected block rule")
	}
}

func TestRenderRejectsInvalid(t *testing.T) {
	c := config.Default("en0", "en0; pass all")
	if _, err := Render(c); err == nil {
		t.Fatal("expected validation error for unsafe interface name")
	}
}

func TestLoadAndFlush(t *testing.T) {
	var calls []string
	Runner = func(stdin []byte, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if len(args) == 1 && args[0] == "-E" {
			return []byte("No ALTQ support in kernel\npf enabled\nToken : 1234567\n"), nil
		}
		return nil, nil
	}
	tok, err := Load(t.TempDir(), "pass all\n")
	if err != nil || tok != "1234567" {
		t.Fatalf("token %q err %v", tok, err)
	}
	if err := Flush(tok); err != nil {
		t.Fatal(err)
	}
	want := []string{"-n -a com.apple/router -f -", "-a com.apple/router -f -", "-E", "-a com.apple/router -F all", "-X 1234567"}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q", calls)
	}
}
