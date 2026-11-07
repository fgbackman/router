import SwiftUI

struct ForwardsTab: View {
    @EnvironmentObject var store: PhoneStore
    @State private var adding = false

    var body: some View {
        NavigationStack {
            NeedsConnection {
                List {
                    let forwards = store.config?.firewall.portForwards ?? []
                    if forwards.isEmpty {
                        ContentUnavailableView {
                            Label("No Port Forwards", systemImage: "arrow.left.arrow.right")
                        } description: {
                            Text("Forward a port to make a device on your network reachable from the internet.")
                        } actions: {
                            Button("Add Port Forward") { adding = true }
                        }
                        .listRowBackground(Color.clear)
                    }
                    ForEach(forwards, id: \.self) { f in
                        ForwardRow(forward: f, deviceName: name(for: f.internalIP))
                    }
                    .onDelete { idx in
                        let doomed = idx.map { forwards[$0] }
                        Task { await store.update { c in c.firewall.portForwards.removeAll { doomed.contains($0) } } }
                    }
                }
                .refreshable { await store.reloadAll() }
            }
            .navigationTitle("Port Forwarding")
            .toolbar {
                if store.isOnline {
                    ToolbarItem(placement: .primaryAction) {
                        Button { adding = true } label: { Label("Add", systemImage: "plus") }
                    }
                }
            }
            .sheet(isPresented: $adding) { AddForwardSheet() }
        }
    }

    private func name(for ip: String) -> String? {
        store.status?.leases.first { $0.ip == ip && !$0.hostname.isEmpty }?.hostname
    }
}

struct ForwardRow: View {
    var forward: RouterConfig.PortForward
    var deviceName: String?

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text(forward.name.isEmpty ? "Port \(forward.externalPort)" : forward.name).font(.body.weight(.medium))
                Text(verbatim: "\(deviceName ?? forward.internalIP):\(forward.internalPort)")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Text(protoLabel)
                .font(.caption2.weight(.bold))
                .padding(.horizontal, 6).padding(.vertical, 3)
                .background(Brand.blue.opacity(0.15), in: Capsule())
                .foregroundStyle(Brand.blue)
            Text(String(forward.externalPort))
                .font(.title3.weight(.semibold).monospacedDigit())
        }
    }

    private var protoLabel: String {
        forward.protocol == "both" ? "TCP+UDP" : forward.protocol.uppercased()
    }
}

struct AddForwardSheet: View {
    @EnvironmentObject var store: PhoneStore
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var proto = "tcp"
    @State private var external = ""
    @State private var ip = ""
    @State private var internalPort = ""

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Name", text: $name, prompt: Text("Web server"))
                    Picker("Protocol", selection: $proto) {
                        Text("TCP").tag("tcp")
                        Text("UDP").tag("udp")
                        Text("Both").tag("both")
                    }
                    .pickerStyle(.segmented)
                    TextField("External port", text: $external, prompt: Text("8080"))
                        .keyboardType(.numberPad)
                }
                Section("To device") {
                    Picker("Device", selection: $ip) {
                        Text("Enter IP below").tag("")
                        ForEach(store.status?.leases ?? []) { l in
                            Text(l.hostname.isEmpty ? l.ip : l.hostname).tag(l.ip)
                        }
                    }
                    TextField("IP address", text: $ip, prompt: Text("192.168.50.10"))
                        .keyboardType(.decimalPad)
                    TextField("Device port", text: $internalPort, prompt: Text("Same as external"))
                        .keyboardType(.numberPad)
                }
                if let e = store.lastError {
                    Section { Text(e).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Add Port Forward")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add") { save() }
                        .disabled(store.busy || Int(external) == nil || ip.isEmpty)
                }
            }
            .onAppear { store.lastError = nil }
        }
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
