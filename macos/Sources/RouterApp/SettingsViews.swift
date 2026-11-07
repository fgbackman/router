import SwiftUI

/// Shared Save/Revert footer for the editable panes.
struct SaveBar: View {
    @EnvironmentObject var store: RouterStore
    var dirty: Bool
    var note: String? = nil
    var revert: () -> Void
    var save: () -> Void

    var body: some View {
        HStack {
            if let note { Text(note).font(.callout).foregroundStyle(.secondary) }
            Spacer()
            if store.busy { ProgressView().controlSize(.small) }
            Button("Revert", action: revert).disabled(!dirty || store.busy)
            Button("Save & Apply", action: save)
                .keyboardShortcut("s")
                .buttonStyle(.borderedProminent)
                .disabled(!dirty || store.busy)
        }
        .padding(12)
        .background(.bar)
    }
}

struct FirewallView: View {
    @EnvironmentObject var store: RouterStore
    @State private var draft: RouterConfig.Firewall?
    @State private var openPorts = ""

    var body: some View {
        VStack(spacing: 0) {
            if draft != nil {
                Form {
                    Section {
                        Toggle("Block unsolicited traffic from the internet", isOn: binding(\.blockInbound))
                        Toggle("Answer ping on the internet side", isOn: binding(\.allowPing))
                            .disabled(!(draft?.blockInbound ?? true))
                    } footer: {
                        Text("Replies to connections your devices start are always allowed. IPv4 only: IPv6 isn't routed.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Section {
                        TextField("Open ports on this Mac", text: $openPorts, prompt: Text("tcp/22 udp/51820"))
                    } footer: {
                        Text("Services on this Mac reachable from the internet, e.g. tcp/22 for SSH. Separate with spaces.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                .formStyle(.grouped)
            }
            Spacer(minLength: 0)
            SaveBar(dirty: dirty, revert: load, save: save)
        }
        .onAppear(perform: load)
        .onChange(of: store.config) { _ in if !dirty { load() } }
    }

    private func binding(_ kp: WritableKeyPath<RouterConfig.Firewall, Bool>) -> Binding<Bool> {
        Binding(get: { draft?[keyPath: kp] ?? false }, set: { draft?[keyPath: kp] = $0 })
    }

    private var parsedPorts: [String] {
        openPorts.split(whereSeparator: { $0 == " " || $0 == "," }).map(String.init)
    }

    private var dirty: Bool {
        guard let draft, let cur = store.config?.firewall else { return false }
        var d = draft
        d.openPorts = parsedPorts
        return d != cur
    }

    private func load() {
        draft = store.config?.firewall
        openPorts = draft?.openPorts.joined(separator: " ") ?? ""
    }

    private func save() {
        guard var d = draft else { return }
        d.openPorts = parsedPorts
        Task { await store.update { $0.firewall = d } }
    }
}

struct DNSView: View {
    @EnvironmentObject var store: RouterStore
    @State private var enabled = true
    @State private var domain = ""
    @State private var upstreams = ""
    @State private var blocked = ""
    @State private var lists = ""
    @State private var hosts = ""

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section("Resolver") {
                    Toggle("Run the DNS server for the local network", isOn: $enabled)
                    TextField("Local domain", text: $domain, prompt: Text("lan"))
                    TextField("Upstream servers", text: $upstreams, prompt: Text("Empty: use this Mac's DNS"))
                }
                Section {
                    TextEditor(text: $hosts).font(.body.monospaced()).frame(minHeight: 70)
                } header: {
                    Text("Local names")
                } footer: {
                    Text("One \"IP name\" per line. DHCP clients are added automatically as name.\(domain.isEmpty ? "lan" : domain).")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    TextEditor(text: $blocked).font(.body.monospaced()).frame(minHeight: 70)
                } header: {
                    Text("Blocked domains")
                } footer: {
                    Text("One per line. Subdomains are blocked too.").font(.caption).foregroundStyle(.secondary)
                }
                Section {
                    TextEditor(text: $lists).font(.body.monospaced()).frame(minHeight: 50)
                } header: {
                    Text("Blocklists")
                } footer: {
                    Text("Hosts-file URLs or file paths, one per line, e.g. https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .formStyle(.grouped)
            SaveBar(dirty: draft != store.config?.dns, revert: load, save: save)
        }
        .onAppear(perform: load)
        .onChange(of: store.config) { _ in if draft == store.config?.dns || draft == nil { load() } }
    }

    private static func lines(_ s: String) -> [String] {
        s.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    /// The edited DNS section, or nil if hosts can't be parsed.
    private var draft: RouterConfig.DNS? {
        guard var d = store.config?.dns else { return nil }
        d.enabled = enabled
        d.domain = domain.trimmingCharacters(in: .whitespaces)
        d.upstreams = upstreams.split(whereSeparator: { $0 == " " || $0 == "," }).map { s in
            let v = String(s)
            return v.contains(":") ? v : v + ":53"
        }
        d.blockedDomains = Self.lines(blocked)
        d.blocklists = Self.lines(lists)
        d.hosts = Self.lines(hosts).compactMap { line in
            let f = line.split(whereSeparator: \.isWhitespace)
            return f.count == 2 ? RouterConfig.HostRecord(name: String(f[1]), ip: String(f[0])) : nil
        }
        return d
    }

    private func load() {
        guard let d = store.config?.dns else { return }
        enabled = d.enabled
        domain = d.domain
        upstreams = d.upstreams.joined(separator: " ")
        blocked = d.blockedDomains.joined(separator: "\n")
        lists = d.blocklists.joined(separator: "\n")
        hosts = d.hosts.map { "\($0.ip) \($0.name)" }.joined(separator: "\n")
    }

    private func save() {
        guard let d = draft else { return }
        Task { await store.update { $0.dns = d } }
    }
}

struct NetworkView: View {
    @EnvironmentObject var store: RouterStore
    @State private var wan = ""
    @State private var lan = ""
    @State private var address = ""
    @State private var configureAddress = true
    @State private var dhcpOn = true
    @State private var start = ""
    @State private var end = ""
    @State private var leaseTime = ""
    @State private var webListen = ""
    @State private var allowLAN = true
    @State private var interfaces: [NetInterface] = []

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section("Ports") {
                    InterfacePicker(title: "Internet (WAN)", selection: $wan, interfaces: interfaces)
                    InterfacePicker(title: "Local network (LAN)", selection: $lan, interfaces: interfaces)
                    TextField("Router address", text: $address, prompt: Text("192.168.50.1/24"))
                    Toggle("Assign this address to the LAN port automatically", isOn: $configureAddress)
                }
                Section("DHCP") {
                    Toggle("Hand out addresses to devices", isOn: $dhcpOn)
                    TextField("First address", text: $start)
                    TextField("Last address", text: $end)
                    TextField("Lease time", text: $leaseTime, prompt: Text("12h"))
                }
                Section {
                    TextField("Admin interface address", text: $webListen, prompt: Text("127.0.0.1:8080"))
                    Toggle("Allow devices on the local network (iPhone app)", isOn: $allowLAN)
                } header: {
                    Text("Admin")
                } footer: {
                    Text("This app connects to the address above. With local network access on, phones and computers on your network can also manage the router at the router address, on the same port, with the admin password.")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            .formStyle(.grouped)
            SaveBar(dirty: dirty, note: "Changing ports or the address briefly interrupts the network.", revert: load, save: save)
        }
        .onAppear {
            load()
            interfaces = (try? ServiceControl.interfaces()) ?? []
        }
    }

    private func apply(_ c: inout RouterConfig) {
        c.wan.interface = wan
        c.lan.interface = lan
        c.lan.address = address.trimmingCharacters(in: .whitespaces)
        c.lan.configureAddress = configureAddress
        c.dhcp.enabled = dhcpOn
        c.dhcp.rangeStart = start.trimmingCharacters(in: .whitespaces)
        c.dhcp.rangeEnd = end.trimmingCharacters(in: .whitespaces)
        c.dhcp.leaseTime = leaseTime.trimmingCharacters(in: .whitespaces)
        c.web.listen = webListen.trimmingCharacters(in: .whitespaces)
        c.web.allowLAN = allowLAN
    }

    private var dirty: Bool {
        guard let cur = store.config else { return false }
        var c = cur
        apply(&c)
        // "12h" and "12h0m0s" are the same duration.
        if c.dhcp.leaseTime != cur.dhcp.leaseTime,
           c.dhcp.leaseTime == cur.dhcp.leaseTime.replacingOccurrences(of: "0m0s", with: "") {
            c.dhcp.leaseTime = cur.dhcp.leaseTime
        }
        return c != cur
    }

    private func load() {
        guard let c = store.config else { return }
        wan = c.wan.interface
        lan = c.lan.interface
        address = c.lan.address
        configureAddress = c.lan.configureAddress
        dhcpOn = c.dhcp.enabled
        start = c.dhcp.rangeStart
        end = c.dhcp.rangeEnd
        leaseTime = c.dhcp.leaseTime.replacingOccurrences(of: "0m0s", with: "")
        webListen = c.web.listen
        allowLAN = c.web.allowLAN
    }

    private func save() {
        let oldListen = store.config?.web.listen
        let newListen = webListen.trimmingCharacters(in: .whitespaces)
        Task {
            let ok = await store.update { apply(&$0) }
            if ok, newListen != oldListen {
                store.urlString = "http://\(newListen)"
            }
        }
    }
}

struct InterfacePicker: View {
    var title: String
    @Binding var selection: String
    var interfaces: [NetInterface]

    var body: some View {
        Picker(title, selection: $selection) {
            if !interfaces.contains(where: { $0.device == selection }) {
                Text(selection.isEmpty ? "Choose…" : selection).tag(selection)
            }
            ForEach(interfaces) { i in
                Text(i.label).tag(i.device)
            }
        }
    }
}

/// First-run setup: pick ports, choose a password, install the service.
struct SetupSheet: View {
    @EnvironmentObject var store: RouterStore
    @Environment(\.dismiss) private var dismiss
    @State private var interfaces: [NetInterface] = []
    @State private var wan = ""
    @State private var lan = ""
    @State private var address = "192.168.50.1/24"
    @State private var password = ""
    @State private var confirm = ""
    @State private var error: String?
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Set up your router").font(.title2.bold())
            Text("Pick the port that connects to the internet and the port your devices plug into (directly, or through a switch or Wi-Fi access point).")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Form {
                InterfacePicker(title: "Internet (WAN)", selection: $wan, interfaces: interfaces)
                InterfacePicker(title: "Devices (LAN)", selection: $lan, interfaces: interfaces)
                TextField("Router address", text: $address)
                SecureField("Admin password", text: $password, prompt: Text("at least 8 characters"))
                SecureField("Confirm password", text: $confirm)
            }
            if ServiceControl.configExists {
                Label("This replaces the existing router configuration.", systemImage: "exclamationmark.triangle")
                    .foregroundStyle(.orange).font(.callout)
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                if working { ProgressView().controlSize(.small) }
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Set Up") { setUp() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(working || !valid)
            }
        }
        .padding(24)
        .frame(width: 520)
        .onAppear(perform: loadInterfaces)
    }

    private var valid: Bool {
        !wan.isEmpty && !lan.isEmpty && wan != lan && password.count >= 8 && password == confirm
    }

    private func loadInterfaces() {
        do {
            interfaces = try ServiceControl.interfaces()
            if wan.isEmpty, let d = interfaces.first(where: { $0.defaultRoute }) { wan = d.device }
        } catch {
            self.error = "Couldn't list network ports: \(error.localizedDescription)"
        }
    }

    private func setUp() {
        working = true
        error = nil
        // AppleScript runs synchronously; let the spinner draw first.
        DispatchQueue.main.async {
            defer { working = false }
            do {
                try ServiceControl.setUp(wan: wan, lan: lan, address: address, password: password)
                store.urlString = "http://127.0.0.1:8080"
                store.user = "admin"
                store.savePassword(password)
                store.config = nil
                dismiss()
            } catch {
                if (error as? ServiceControl.Failure)?.message != "Cancelled." {
                    self.error = error.localizedDescription
                }
            }
        }
    }
}
