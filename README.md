# router

Turn a Mac into a home router, in the spirit of DD-WRT, using only what
macOS already ships:

- **NAT and firewall** via `pf`: blocks unsolicited inbound traffic, with port
  forwarding and optional open ports on the Mac itself
- **DHCP server** with static leases, persistent leases and hostname registration
- **DNS forwarder** that resolves `<hostname>.lan` for DHCP clients, supports
  your own host records and blocks domains (hosts-file or URL blocklists)
- **Web admin** on Status, Setup, DHCP, NAT/Firewall, DNS and Packet Filter pages
- **LaunchDaemon** support so it starts at boot and restarts if it dies

It's a single Go binary with no dependencies, and it runs on Apple Silicon and Intel Macs.

```
 Internet ── modem ──[en0  WAN]  Mac  [en5  LAN]── switch / Wi-Fi AP ── your devices
                                  │
                       pf NAT + firewall, DHCP, DNS, web UI
```

## What you need

- macOS 12 or newer
- **Two network interfaces.** One faces the internet (WAN). The other serves
  your network (LAN), usually a USB or Thunderbolt Ethernet adapter. For Wi-Fi
  clients, plug a Wi-Fi access point (or an old router in AP/bridge mode)
  into the LAN port. macOS can't run a proper Wi-Fi access point itself.
- **Internet Sharing turned off** (System Settings › General › Sharing). It runs
  its own DHCP server and NAT, which conflict with this tool.

## Install

```bash
git clone https://github.com/fgbackman/router && cd router
make universal   # or `make build` for just this Mac's architecture
sudo make install
```

That puts `router` in `/usr/local/bin`. Building needs Go 1.24+ (`brew install go`).

## Set up

1. Find your interfaces:

   ```bash
   router interfaces
   ```

   ```
   DEVICE  PORT                          STATE  IPv4            MAC
   en0     Ethernet                      up     10.0.1.23/24    …  <- internet (default route)
   en5     Thunderbolt Ethernet Slot 1   up                     …
   ```

2. Write a config. The WAN defaults to the interface with the default route:

   ```bash
   sudo router init -lan en5
   ```

3. Set a password for the web admin:

   ```bash
   sudo router passwd
   ```

4. Review the firewall rules (pfctl checks them too):

   ```bash
   sudo router check
   ```

5. Try it in the foreground. Ctrl-C restores everything:

   ```bash
   sudo router run
   ```

6. Once it works, install it to start at boot:

   ```bash
   sudo router install
   ```

## Day to day

| Command | What it does |
|---|---|
| `router status` | Service state, addresses, DHCP clients and pf rules (with sudo) |
| `sudo router reload` | Reread the config file without dropping the network |
| `sudo router uninstall` | Stop the service and restore the system |
| `sudo router down` | Undo leftover changes after a crash (forwarding, LAN address, pf rules) |
| `tail -f /var/log/router.log` | Logs from the LaunchDaemon |

The web admin listens on `http://127.0.0.1:8080/` by default, which only this
Mac can reach. To manage the router from the LAN, set `web.listen` to the LAN
address, e.g. `"192.168.50.1:8080"`. The WAN side can't reach it while
`blockInbound` is on.

## Configuration

`/usr/local/etc/router/config.json` (written with mode 0600):

```json
{
  "wan": { "interface": "en0" },
  "lan": { "interface": "en5", "address": "192.168.50.1/24", "configureAddress": true },
  "dhcp": {
    "enabled": true,
    "rangeStart": "192.168.50.100",
    "rangeEnd": "192.168.50.249",
    "leaseTime": "12h",
    "staticLeases": [{ "mac": "aa:bb:cc:dd:ee:ff", "ip": "192.168.50.20", "hostname": "nas" }]
  },
  "dns": {
    "enabled": true,
    "domain": "lan",
    "upstreams": ["1.1.1.1:53", "9.9.9.9:53"],
    "blocklists": ["https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts"],
    "blockedDomains": ["doubleclick.net"],
    "hosts": [{ "name": "printer", "ip": "192.168.50.30" }]
  },
  "firewall": {
    "blockInbound": true,
    "allowPing": false,
    "openPorts": ["tcp/22"],
    "portForwards": [
      { "name": "Plex", "protocol": "tcp", "externalPort": 32400, "internalIP": "192.168.50.20", "internalPort": 32400 }
    ]
  },
  "web": { "enabled": true, "listen": "127.0.0.1:8080", "user": "admin", "passwordHash": "…" },
  "stateDir": "/var/db/router"
}
```

Notes:

- `lan.configureAddress` adds the router address to the LAN interface as an
  alias and removes it on stop. Turn it off if you'd rather set a manual IP for
  that interface in System Settings.
- An empty `dns.upstreams` list means the router uses whatever DNS the Mac got
  from the WAN (`/etc/resolv.conf`) and picks up changes automatically.
- Blocklists are downloaded at startup and every 24 hours, and cached in
  `stateDir` so blocking keeps working when the internet is down. Blocked names
  resolve to `0.0.0.0` / `::`.
- Names under the local domain and reverse lookups for the LAN are never sent upstream.

## How it works

| Piece | Implementation |
|---|---|
| Forwarding | `sysctl net.inet.ip.forwarding=1`. The previous value is restored on stop. |
| NAT / firewall | Rules go into the pf anchor `com.apple/router`. The stock `/etc/pf.conf` already evaluates `com.apple/*`, so **no system files are edited**. pf is enabled with a reference token (`pfctl -E`/`-X`), so turning the router off doesn't disable pf for anything else that uses it. The rendered rules are saved in `stateDir/pf.rules`. |
| DHCP | Built-in RFC 2131 server on UDP 67, bound to the LAN interface with `IP_BOUND_IF`. Leases are kept in `stateDir/leases.json`. |
| DNS | Built-in forwarder on the LAN address, UDP and TCP port 53. |
| Crash safety | Each system change is recorded in `stateDir/state.json` before it's made. The next start (or `router down`) undoes whatever a crashed run left behind. |

## Limitations

- IPv4 only. IPv6 isn't routed, and IPv6 traffic keeps the normal macOS firewall behaviour.
- One LAN. No VLANs, no multi-WAN failover.
- No Wi-Fi access point mode (see "What you need").
- No QoS or traffic shaping yet. pf on macOS supports dummynet, so this could be added later.

## Development

```bash
make test   # go vet + race-enabled tests (they also run on Linux)
```

CI runs on `macos-latest`. It builds the universal binary and has the real
`pfctl` parse a generated ruleset with NAT, port forwards and firewall rules.
