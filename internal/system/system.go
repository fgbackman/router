// Package system wraps the macOS commands the router drives: sysctl,
// ifconfig, networksetup, route and launchctl.
package system

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes a command and returns its combined output. Tests replace it.
var Run = func(name string, args ...string) ([]byte, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

const forwardingKey = "net.inet.ip.forwarding"

// Forwarding reports the current IPv4 forwarding sysctl ("0" or "1").
func Forwarding() (string, error) {
	out, err := Run("/usr/sbin/sysctl", "-n", forwardingKey)
	return strings.TrimSpace(string(out)), err
}

func SetForwarding(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	_, err := Run("/usr/sbin/sysctl", "-w", forwardingKey+"="+v)
	return err
}

// HasAddress reports whether iface already carries addr.
func HasAddress(iface string, addr netip.Addr) bool {
	for _, a := range IPv4Addrs(iface) {
		if a.Addr() == addr {
			return true
		}
	}
	return false
}

// IPv4Addrs lists the IPv4 addresses on iface.
func IPv4Addrs(iface string) []netip.Prefix {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil
	}
	addrs, _ := ifi.Addrs()
	var out []netip.Prefix
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP.To4()); ok && n.IP.To4() != nil {
				ones, _ := n.Mask.Size()
				out = append(out, netip.PrefixFrom(ip, ones))
			}
		}
	}
	return out
}

// AddAlias adds p to iface. It returns false if the address was already there.
func AddAlias(iface string, p netip.Prefix) (bool, error) {
	if HasAddress(iface, p.Addr()) {
		return false, nil
	}
	mask := net.IP(net.CIDRMask(p.Bits(), 32)).String()
	_, err := Run("/sbin/ifconfig", iface, "inet", p.Addr().String(), "netmask", mask, "alias")
	return err == nil, err
}

func RemoveAlias(iface string, a netip.Addr) error {
	_, err := Run("/sbin/ifconfig", iface, "inet", a.String(), "-alias")
	return err
}

// DefaultRouteInterface returns the interface of the default route, e.g. "en0".
func DefaultRouteInterface() string {
	out, err := Run("/sbin/route", "-n", "get", "default")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "interface" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Interface describes a network device for `router interfaces`.
type Interface struct {
	Device string
	Port   string // macOS hardware port name, e.g. "Thunderbolt Ethernet Slot 1"
	MAC    string
	Up     bool
	Addrs  []netip.Prefix
}

// Interfaces lists hardware network ports with their current state.
func Interfaces() []Interface {
	ports := map[string]string{}
	if out, err := Run("/usr/sbin/networksetup", "-listallhardwareports"); err == nil {
		ports = parseHardwarePorts(out)
	}
	ifs, _ := net.Interfaces()
	var out []Interface
	for _, ifi := range ifs {
		port, known := ports[ifi.Name]
		if !known && !strings.HasPrefix(ifi.Name, "en") && !strings.HasPrefix(ifi.Name, "bridge") {
			continue
		}
		out = append(out, Interface{
			Device: ifi.Name, Port: port, MAC: ifi.HardwareAddr.String(),
			Up: ifi.Flags&net.FlagUp != 0, Addrs: IPv4Addrs(ifi.Name),
		})
	}
	return out
}

func parseHardwarePorts(out []byte) map[string]string {
	m := map[string]string{}
	var port string
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "Hardware Port: "); ok {
			port = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "Device: "); ok {
			m[strings.TrimSpace(v)] = port
		}
	}
	return m
}

// InternetSharingActive reports whether macOS Internet Sharing is running.
// It conflicts with the router: both want UDP 67/53 and pf NAT.
func InternetSharingActive() bool {
	_, err := Run("/usr/bin/pgrep", "-x", "InternetSharing")
	return err == nil
}

// State records what Up changed so Down can undo exactly that, even after a crash.
type State struct {
	PFToken        string     `json:"pfToken"`
	PrevForwarding string     `json:"prevForwarding"`
	AliasIface     string     `json:"aliasIface,omitempty"`
	AliasAddr      netip.Addr `json:"aliasAddr,omitzero"`
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }

func LoadState(dir string) (*State, error) {
	b, err := os.ReadFile(statePath(dir))
	if err != nil {
		return nil, err
	}
	s := &State{}
	return s, json.Unmarshal(b, s)
}

func (s *State) Save(dir string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(dir), b, 0o600)
}

func RemoveState(dir string) error {
	err := os.Remove(statePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// --- launchd ---

const (
	Label     = "com.fgbackman.router"
	PlistPath = "/Library/LaunchDaemons/" + Label + ".plist"
	LogPath   = "/var/log/router.log"
)

func plist(exe, configPath string) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		var x bytes.Buffer
		_ = xmlEscape(&x, s)
		return x.String()
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>run</string>
		<string>-config</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, Label, esc(exe), esc(configPath), LogPath, LogPath)
	return b.Bytes()
}

func xmlEscape(b *bytes.Buffer, s string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	_, err := b.WriteString(r.Replace(s))
	return err
}

// Install writes the LaunchDaemon plist and starts the service.
func Install(exe, configPath string) error {
	if Installed() {
		_, _ = Run("/bin/launchctl", "bootout", "system/"+Label)
	}
	if err := os.WriteFile(PlistPath, plist(exe, configPath), 0o644); err != nil {
		return err
	}
	_, _ = Run("/bin/launchctl", "enable", "system/"+Label)
	_, err := Run("/bin/launchctl", "bootstrap", "system", PlistPath)
	return err
}

// Uninstall stops the service and removes the plist.
func Uninstall() error {
	_, err := Run("/bin/launchctl", "bootout", "system/"+Label)
	if rmErr := os.Remove(PlistPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return rmErr
	}
	return err
}

func Installed() bool {
	_, err := os.Stat(PlistPath)
	return err == nil
}

// ServiceRunning reports whether launchd has the job loaded.
func ServiceRunning() bool {
	_, err := Run("/bin/launchctl", "print", "system/"+Label)
	return err == nil
}

// Restart asks launchd to restart the service (kickstart -k).
func Restart() error {
	_, err := Run("/bin/launchctl", "kickstart", "-k", "system/"+Label)
	return err
}

// Reload sends SIGHUP to the running service so it rereads its config.
func Reload() error {
	_, err := Run("/bin/launchctl", "kill", "HUP", "system/"+Label)
	return err
}

// Start loads the installed LaunchDaemon.
func Start() error {
	if !Installed() {
		return errors.New("not installed; run `sudo router install` first")
	}
	if ServiceRunning() {
		return nil
	}
	_, err := Run("/bin/launchctl", "bootstrap", "system", PlistPath)
	return err
}

// Stop unloads the LaunchDaemon until the next Start or reboot. The daemon
// restores forwarding, the LAN address and pf rules as it exits.
func Stop() error {
	if !ServiceRunning() {
		return nil
	}
	_, err := Run("/bin/launchctl", "bootout", "system/"+Label)
	return err
}
