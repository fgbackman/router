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
| `sudo router start` / `stop` | Start or stop the installed service (a stop lasts until reboot) |
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

## Mac app

`macos/` holds a native SwiftUI app for managing the router. It has a main
window and a menu bar icon, and covers:

- **Status:** WAN and LAN addresses, DNS counters and any service errors
- **Clients:** devices on the network; right-click one to reserve its address
- **Port Forwarding, Firewall, DNS, Network:** edit and apply the settings live
- **Set Up Router…:** pick the internet and LAN ports and a password; the app
  writes the config and installs the service
- **Start, Stop and Restart** from the toolbar or the menu bar

Build and install it (needs Xcode or the Command Line Tools):

```bash
make install-app
```

The app needs the `router` command in `/usr/local/bin` (see Install above). It
talks to the running service through the admin API at `web.listen`, keeps
the admin password in your keychain, and uses the standard macOS
administrator prompt for anything that needs root (setup, start, stop).

The API it uses is also available to scripts:

| Request | Result |
|---|---|
| `GET /api/status` | Status, leases and DNS counters |
| `GET /api/config` | The config, without the password hash |
| `PUT /api/config` | Validate, save and apply a full config. Requires an `X-Router-Client` header. |

All requests use the admin user and password (HTTP Basic auth).

## iPhone app

`ios/` holds an iPhone (and iPad) app for the same router. It has four tabs:

- **Status:** whether the router is up, how many devices are connected, DNS blocking stats and network details
- **Devices:** everything on the network; swipe left to reserve a device's address
- **Ports:** add or remove port forwards
- **Settings:** firewall and DNS switches and blocked domains

The phone talks to the router over your local network, so:

1. On the Mac, turn on **Network › Allow devices on the local network** in the
   Mac app (or set `"allowLAN": true` under `web` in the config). New configs
   have it on already.
2. Connect the phone to the router's network: Wi-Fi from an access point
   plugged into the router's LAN port.
3. In the app, the router address is `http://192.168.50.1:8080`. Sign in with
   the admin user and password.

Starting and stopping the router, and changing its ports, stays in the Mac app.

**Installing it on your phone from Terminal:** install Xcode, open it once and
add your Apple ID under **Xcode › Settings › Accounts**. Then plug in your
iPhone (unlock it and tap **Trust**) and run:

```bash
./ios/install.sh
```

It finds your Apple team and the connected iPhone, then builds, signs,
installs and opens the app. The first time, iOS asks you to trust your
developer account (**Settings › General › VPN & Device Management**) and may
ask you to turn on **Developer Mode** (**Settings › Privacy & Security**).

**Or from Xcode:** open `ios/Router.xcodeproj` in Xcode (16 or
newer). Select the **Router** target, open **Signing & Capabilities**, and pick
your team. A free Apple ID works; if Xcode says the bundle identifier is taken,
change it to something unique such as `com.yourname.router`. Plug in your
iPhone, choose it as the run destination and press Run. The first time,
approve the developer on the phone under **Settings › General › VPN & Device
Management**. Apps signed with a free Apple ID stop opening after 7 days; run
them from Xcode again to renew.

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
