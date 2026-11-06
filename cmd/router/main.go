// Command router turns a Mac into a NAT router: pf firewall and port
// forwarding, a DHCP server, a DNS forwarder with blocking, and a web UI.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/fgbackman/router/internal/config"
	"github.com/fgbackman/router/internal/daemon"
	"github.com/fgbackman/router/internal/dhcp"
	"github.com/fgbackman/router/internal/pf"
	"github.com/fgbackman/router/internal/system"
)

var version = "dev"

const usage = `router - turn this Mac into a home router

Usage: sudo router <command> [flags]

Setup:
  interfaces           list network devices (pick WAN and LAN from here)
  init                 write a starter config  (-wan en0 -lan en5)
  passwd               set the web admin password
  check                validate the config and show the pf rules

Running:
  run                  run in the foreground (Ctrl-C restores the system)
  install              install and start as a LaunchDaemon (starts at boot)
  start / stop         start or stop the installed LaunchDaemon (stop lasts until reboot)
  uninstall            stop and remove the LaunchDaemon
  reload               make the running service reread its config
  status               show what's running
  down                 undo leftover changes after a crash

Every command accepts -config PATH (default ` + config.DefaultPath + `).
`

func main() {
	log.SetFlags(0)
	daemon.Version = version
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "interfaces":
		err = cmdInterfaces(args)
	case "init":
		err = cmdInit(args)
	case "passwd":
		err = cmdPasswd(args)
	case "check":
		err = cmdCheck(args)
	case "run":
		err = cmdRun(args)
	case "install":
		err = cmdInstall(args)
	case "uninstall":
		err = needRoot(system.Uninstall)
	case "start":
		err = needRoot(system.Start)
	case "stop":
		err = needRoot(system.Stop)
	case "reload":
		err = needRoot(system.Reload)
	case "status":
		err = cmdStatus(args)
	case "down":
		err = cmdDown(args)
	case "version", "-v", "--version":
		fmt.Println("router", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("router %s: %v", cmd, err)
	}
}

func flags(name string, args []string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	path := fs.String("config", config.DefaultPath, "config file")
	return fs, path
}

func requireMac() error {
	if runtime.GOOS != "darwin" {
		return errors.New("this command only works on macOS")
	}
	return nil
}

func requireRoot() error {
	if err := requireMac(); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("needs root; run it with sudo")
	}
	return nil
}

func needRoot(f func() error) error {
	if err := requireRoot(); err != nil {
		return err
	}
	return f()
}

func cmdInterfaces(args []string) error {
	fs := flag.NewFlagSet("interfaces", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON (used by the Mac app)")
	fs.Parse(args)
	if err := requireMac(); err != nil {
		return err
	}
	def := system.DefaultRouteInterface()
	if *asJSON {
		type row struct {
			Device       string   `json:"device"`
			Port         string   `json:"port"`
			MAC          string   `json:"mac"`
			Up           bool     `json:"up"`
			Addresses    []string `json:"addresses"`
			DefaultRoute bool     `json:"defaultRoute"`
		}
		rows := []row{}
		for _, i := range system.Interfaces() {
			as := []string{}
			for _, a := range i.Addrs {
				as = append(as, a.String())
			}
			rows = append(rows, row{i.Device, i.Port, i.MAC, i.Up, as, i.Device == def})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DEVICE\tPORT\tSTATE\tIPv4\tMAC\t")
	for _, i := range system.Interfaces() {
		state := "down"
		if i.Up {
			state = "up"
		}
		var as []string
		for _, a := range i.Addrs {
			as = append(as, a.String())
		}
		note := ""
		if i.Device == def {
			note = "<- internet (default route)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", i.Device, i.Port, state, strings.Join(as, ","), i.MAC, note)
	}
	return tw.Flush()
}

func cmdInit(args []string) error {
	fs, path := flags("init", args)
	wan := fs.String("wan", "", "WAN (internet) interface; default: current default route")
	lan := fs.String("lan", "", "LAN interface to serve, e.g. en5")
	addr := fs.String("addr", "192.168.50.1/24", "router LAN address")
	force := fs.Bool("force", false, "overwrite an existing config")
	fs.Parse(args)
	if err := requireRoot(); err != nil {
		return err
	}
	if _, err := os.Stat(*path); err == nil && !*force {
		return fmt.Errorf("%s exists; pass -force to overwrite", *path)
	}
	if *wan == "" {
		*wan = system.DefaultRouteInterface()
	}
	if *lan == "" || *wan == "" {
		cmdInterfaces(nil)
		return errors.New("pass -lan DEVICE (and -wan DEVICE if no default route) from the list above")
	}
	c := config.Default(*wan, *lan)
	c.LAN.Address = *addr
	if p, err := c.LANPrefix(); err == nil {
		// Shift the default DHCP range into the chosen subnet.
		base := p.Masked().Addr().As4()
		if p.Bits() == 24 {
			c.DHCP.RangeStart = fmt.Sprintf("%d.%d.%d.100", base[0], base[1], base[2])
			c.DHCP.RangeEnd = fmt.Sprintf("%d.%d.%d.249", base[0], base[1], base[2])
		}
	}
	if err := c.Save(*path); err != nil {
		return err
	}
	fmt.Printf("Wrote %s (WAN %s, LAN %s %s).\n\nNext:\n  sudo router passwd     # set the web admin password\n  sudo router check      # review the firewall rules\n  sudo router run        # try it in the foreground\n  sudo router install    # then run it at boot\n", *path, *wan, *lan, c.LAN.Address)
	return nil
}

func cmdPasswd(args []string) error {
	fs, path := flags("passwd", args)
	stdin := fs.Bool("stdin", false, "read the password from stdin without prompting")
	fs.Parse(args)
	if err := requireRoot(); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	var pw string
	if *stdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
		if err != nil {
			return err
		}
		pw = strings.TrimRight(string(b), "\r\n")
	} else {
		p1, err := prompt("New admin password: ")
		if err != nil {
			return err
		}
		p2, err := prompt("Again: ")
		if err != nil {
			return err
		}
		if p1 != p2 {
			return errors.New("passwords don't match")
		}
		pw = p1
	}
	if len(pw) < 8 {
		return errors.New("use at least 8 characters")
	}
	if c.Web.PasswordHash, err = config.HashPassword(pw); err != nil {
		return err
	}
	if err := c.Save(*path); err != nil {
		return err
	}
	fmt.Printf("Password set for user %q.\n", c.Web.User)
	if system.ServiceRunning() {
		return system.Reload()
	}
	return nil
}

// prompt reads a line with terminal echo off.
func prompt(msg string) (string, error) {
	fmt.Fprint(os.Stderr, msg)
	stty := func(arg string) {
		c := exec.Command("/bin/stty", arg)
		c.Stdin = os.Stdin
		_ = c.Run()
	}
	stty("-echo")
	defer func() { stty("echo"); fmt.Fprintln(os.Stderr) }()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func cmdCheck(args []string) error {
	fs, path := flags("check", args)
	fs.Parse(args)
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	rules, err := pf.Render(c)
	if err != nil {
		return err
	}
	fmt.Print(rules)
	if runtime.GOOS == "darwin" && os.Geteuid() == 0 {
		if err := pf.Check(rules); err != nil {
			return err
		}
		fmt.Println("\npfctl: rules OK")
	} else {
		fmt.Println("\n(run with sudo on macOS to have pfctl verify the rules)")
	}
	if c.Web.Enabled && c.Web.PasswordHash == "" {
		fmt.Println("note: no web password set; run `sudo router passwd`")
	}
	return nil
}

func cmdRun(args []string) error {
	fs, path := flags("run", args)
	fs.Parse(args)
	if err := requireRoot(); err != nil {
		return err
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	return daemon.New(*path, logger).Run(ctx, hup)
}

func cmdInstall(args []string) error {
	fs, path := flags("install", args)
	fs.Parse(args)
	if err := requireRoot(); err != nil {
		return err
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	if _, err := config.Load(abs); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if strings.HasPrefix(exe, "/Users/") || strings.HasPrefix(exe, os.TempDir()) {
		fmt.Fprintf(os.Stderr, "warning: %s is in a user or temp folder; consider `sudo make install` to put it in /usr/local/bin first\n", exe)
	}
	if err := system.Install(exe, abs); err != nil {
		return err
	}
	fmt.Printf("Installed %s; logs in %s.\n", system.PlistPath, system.LogPath)
	return nil
}

func cmdStatus(args []string) error {
	fs, path := flags("status", args)
	fs.Parse(args)
	if err := requireMac(); err != nil {
		return err
	}
	c, err := config.Load(*path)
	if err != nil {
		return err
	}
	fw, _ := system.Forwarding()
	fmt.Printf("service:     installed=%v running=%v\n", system.Installed(), system.ServiceRunning())
	fmt.Printf("forwarding:  %s\n", fw)
	fmt.Printf("WAN:         %s %v\n", c.WAN.Interface, system.IPv4Addrs(c.WAN.Interface))
	fmt.Printf("LAN:         %s %v\n", c.LAN.Interface, system.IPv4Addrs(c.LAN.Interface))
	if c.Web.Enabled {
		fmt.Printf("admin UI:    http://%s/\n", c.Web.Listen)
	}
	if system.InternetSharingActive() {
		fmt.Println("warning:     macOS Internet Sharing is on and will conflict")
	}
	if pool, err := dhcp.NewPool(c, filepath.Join(c.StateDir, "leases.json")); err == nil {
		ls := pool.Active()
		fmt.Printf("\nDHCP clients (%d):\n", len(ls))
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		for _, l := range ls {
			exp := "static"
			if !l.Static {
				exp = l.Expires.Local().Format("Jan 2 15:04")
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", l.IP, l.MAC, l.Hostname, exp)
		}
		tw.Flush()
	}
	if os.Geteuid() == 0 {
		fmt.Printf("\n%s", pf.Status())
	}
	return nil
}

func cmdDown(args []string) error {
	fs, path := flags("down", args)
	fs.Parse(args)
	if err := requireRoot(); err != nil {
		return err
	}
	stateDir := config.DefaultStateDir
	if c, err := config.Load(*path); err == nil {
		stateDir = c.StateDir
	}
	if system.ServiceRunning() {
		return errors.New("the LaunchDaemon is running; use `sudo router uninstall` to stop it")
	}
	st, err := system.LoadState(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("nothing to undo")
		return nil
	} else if err != nil {
		return err
	}
	daemon.Teardown(stateDir, st, log.New(os.Stderr, "", 0))
	fmt.Println("restored forwarding, LAN address and pf rules")
	return nil
}
