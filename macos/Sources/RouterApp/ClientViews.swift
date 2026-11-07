import SwiftUI

struct ClientsView: View {
    @EnvironmentObject var store: RouterStore
    @State private var selection: Lease.ID?
    @State private var reserving: Lease?
    @State private var adding = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Table(store.status?.leases ?? [], selection: $selection) {
                TableColumn("Name") { l in
                    Text(l.hostname.isEmpty ? "—" : l.hostname)
                }
                TableColumn("IP address") { l in Text(l.ip).monospacedDigit().textSelection(.enabled) }
                TableColumn("MAC address") { l in Text(l.mac).font(.body.monospaced()).textSelection(.enabled) }
                TableColumn("Lease") { l in
                    if l.isStatic {
                        Text("Reserved").foregroundStyle(.secondary)
                    } else if let e = l.expires {
                        Text(e, style: .relative)
                    } else {
                        Text("—")
                    }
                }
            }
            .contextMenu(forSelectionType: Lease.ID.self) { ids in
                if let id = ids.first, let l = store.status?.leases.first(where: { $0.id == id }), !l.isStatic {
                    Button("Reserve This Address…") { reserving = l }
                }
            }

            Divider()
            ReservationsList(adding: $adding)
                .frame(height: 220)
        }
        .sheet(item: $reserving) { l in
            StaticLeaseSheet(initial: .init(mac: l.mac, ip: l.ip, hostname: l.hostname))
        }
        .sheet(isPresented: $adding) {
            StaticLeaseSheet(initial: .init(mac: "", ip: "", hostname: ""))
        }
    }
}

/// Static DHCP leases ("reservations").
struct ReservationsList: View {
    @EnvironmentObject var store: RouterStore
    @Binding var adding: Bool

    var body: some View {
        let leases = store.config?.dhcp.staticLeases ?? []
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Reserved addresses").font(.headline)
                Spacer()
                Button {
                    adding = true
                } label: {
                    Label("Add", systemImage: "plus")
                }
            }
            if leases.isEmpty {
                Text("Devices always get the same address when you reserve one. Right-click a client above to reserve its current address.")
                    .foregroundStyle(.secondary).font(.callout)
                Spacer()
            } else {
                Table(leases) {
                    TableColumn("Name", value: \.hostname)
                    TableColumn("IP address") { l in Text(l.ip).monospacedDigit() }
                    TableColumn("MAC address") { l in Text(l.mac).font(.body.monospaced()) }
                    TableColumn("") { l in
                        Button(role: .destructive) {
                            Task { await store.update { $0.dhcp.staticLeases.removeAll { $0 == l } } }
                        } label: {
                            Image(systemName: "trash")
                        }
                        .buttonStyle(.borderless)
                        .help("Remove reservation")
                    }
                    .width(30)
                }
            }
        }
        .padding(16)
    }
}

extension RouterConfig.StaticLease: Identifiable {
    var id: String { mac }
}

struct StaticLeaseSheet: View {
    @EnvironmentObject var store: RouterStore
    @Environment(\.dismiss) private var dismiss
    @State var initial: RouterConfig.StaticLease

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Reserve an address").font(.title3.bold())
            Form {
                TextField("MAC address", text: $initial.mac, prompt: Text("aa:bb:cc:dd:ee:ff"))
                TextField("IP address", text: $initial.ip, prompt: Text("192.168.50.20"))
                TextField("Name", text: $initial.hostname, prompt: Text("nas"))
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save") {
                    let lease = initial
                    Task {
                        if await store.update({ c in
                            c.dhcp.staticLeases.removeAll { $0.mac.lowercased() == lease.mac.lowercased() }
                            c.dhcp.staticLeases.append(lease)
                        }) {
                            dismiss()
                        }
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(store.busy)
            }
            if let e = store.lastError {
                Text(e).foregroundStyle(.red).font(.callout).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(20)
        .frame(width: 420)
        .onAppear { store.lastError = nil }
    }
}

struct PortForwardsView: View {
    @EnvironmentObject var store: RouterStore
    @State private var adding = false

    var body: some View {
        let forwards = store.config?.firewall.portForwards ?? []
        VStack(alignment: .leading, spacing: 0) {
            if forwards.isEmpty {
                VStack(spacing: 10) {
                    Image(systemName: "arrow.left.arrow.right").font(.largeTitle).foregroundStyle(.secondary)
                    Text("No port forwards").font(.headline)
                    Text("Forward a port to make a device on your network reachable from the internet.")
                        .foregroundStyle(.secondary)
                    Button("Add Port Forward…") { adding = true }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Table(forwards) {
                    TableColumn("Name", value: \.name)
                    TableColumn("Protocol") { f in Text(f.protocol.uppercased()) }
                    TableColumn("External port") { f in Text(String(f.externalPort)).monospacedDigit() }
                    TableColumn("Device") { f in Text(verbatim: "\(f.internalIP):\(f.internalPort)").monospacedDigit() }
                    TableColumn("") { f in
                        Button(role: .destructive) {
                            Task { await store.update { $0.firewall.portForwards.removeAll { $0 == f } } }
                        } label: {
                            Image(systemName: "trash")
                        }
                        .buttonStyle(.borderless)
                    }
                    .width(30)
                }
            }
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button { adding = true } label: { Label("Add Port Forward", systemImage: "plus") }
            }
        }
        .sheet(isPresented: $adding) { PortForwardSheet() }
    }
}

extension RouterConfig.PortForward: Identifiable {
    var id: String { "\(self.protocol)/\(externalPort)" }
}

struct PortForwardSheet: View {
    @EnvironmentObject var store: RouterStore
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var proto = "tcp"
    @State private var external = ""
    @State private var ip = ""
    @State private var internalPort = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Add port forward").font(.title3.bold())
            Form {
                TextField("Name", text: $name, prompt: Text("Web server"))
                Picker("Protocol", selection: $proto) {
                    Text("TCP").tag("tcp")
                    Text("UDP").tag("udp")
                    Text("TCP + UDP").tag("both")
                }
                TextField("External port", text: $external, prompt: Text("8080"))
                Picker("Device", selection: $ip) {
                    Text("Choose…").tag("")
                    ForEach(store.status?.leases ?? []) { l in
                        Text(l.hostname.isEmpty ? l.ip : "\(l.hostname) (\(l.ip))").tag(l.ip)
                    }
                }
                TextField("…or IP address", text: $ip, prompt: Text("192.168.50.10"))
                TextField("Device port", text: $internalPort, prompt: Text("same as external"))
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Add") { save() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(store.busy || Int(external) == nil || ip.isEmpty)
            }
            if let e = store.lastError {
                Text(e).foregroundStyle(.red).font(.callout).fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(20)
        .frame(width: 440)
        .onAppear { store.lastError = nil }
    }

    private func save() {
        guard let ext = Int(external) else { return }
        let f = RouterConfig.PortForward(
            name: name.trimmingCharacters(in: .whitespaces),
            protocol: proto,
            externalPort: ext,
            internalIP: ip.trimmingCharacters(in: .whitespaces),
            internalPort: Int(internalPort) ?? ext
        )
        Task {
            if await store.update({ $0.firewall.portForwards.append(f) }) { dismiss() }
        }
    }
}
