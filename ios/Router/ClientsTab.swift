import SwiftUI

struct ClientsTab: View {
    @EnvironmentObject var store: PhoneStore
    @State private var reserving: RouterConfig.StaticLease?

    var body: some View {
        NavigationStack {
            NeedsConnection {
                List {
                    let leases = store.status?.leases ?? []
                    Section {
                        if leases.isEmpty {
                            Text("No devices yet. Plug something into the router's network.")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(leases) { l in
                            DeviceRow(lease: l)
                                .swipeActions {
                                    if l.isStatic {
                                        Button("Unreserve", role: .destructive) { unreserve(l.mac) }
                                    } else {
                                        Button("Reserve") {
                                            reserving = .init(mac: l.mac, ip: l.ip, hostname: l.hostname)
                                        }
                                        .tint(Brand.blue)
                                    }
                                }
                        }
                    } header: {
                        Text("\(leases.count) connected")
                    } footer: {
                        Text("Swipe left on a device to reserve its address, so it always gets the same one.")
                    }
                }
                .refreshable { await store.reloadAll() }
            }
            .navigationTitle("Devices")
            .sheet(item: $reserving) { lease in
                ReserveSheet(lease: lease)
            }
        }
    }

    private func unreserve(_ mac: String) {
        Task {
            await store.update { c in
                c.dhcp.staticLeases.removeAll { $0.mac.lowercased() == mac.lowercased() }
            }
        }
    }
}

extension RouterConfig.StaticLease: Identifiable {
    var id: String { mac }
}

struct DeviceRow: View {
    var lease: Lease

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: icon)
                .font(.title3)
                .foregroundStyle(Brand.blue)
                .frame(width: 34, height: 34)
                .background(Brand.blue.opacity(0.12), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(lease.hostname.isEmpty ? "Unknown device" : lease.hostname)
                    .font(.body.weight(.medium))
                Text(lease.mac).font(.caption.monospaced()).foregroundStyle(.secondary)
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 2) {
                Text(lease.ip).font(.callout.monospacedDigit())
                if lease.isStatic {
                    Label("Reserved", systemImage: "pin.fill").font(.caption2).foregroundStyle(Brand.blue)
                } else if let e = lease.expires {
                    Text(e, style: .relative).font(.caption2).foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 2)
    }

    /// A guess from the hostname; DHCP doesn't say what kind of device it is.
    private var icon: String {
        let h = lease.hostname.lowercased()
        if h.contains("iphone") || h.contains("android") || h.contains("phone") || h.contains("pixel") { return "iphone" }
        if h.contains("ipad") || h.contains("tablet") { return "ipad" }
        if h.contains("tv") || h.contains("roku") || h.contains("chromecast") { return "tv" }
        if h.contains("mac") || h.contains("laptop") || h.contains("book") || h.contains("pc") { return "laptopcomputer" }
        if h.contains("nas") || h.contains("server") || h.contains("synology") { return "externaldrive.connected.to.line.below" }
        if h.contains("print") { return "printer" }
        if h.contains("watch") { return "applewatch" }
        if h.contains("speaker") || h.contains("sonos") || h.contains("homepod") || h.contains("echo") { return "hifispeaker" }
        return "network"
    }
}

struct ReserveSheet: View {
    @EnvironmentObject var store: PhoneStore
    @Environment(\.dismiss) private var dismiss
    @State var lease: RouterConfig.StaticLease

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Name", text: $lease.hostname)
                        .textInputAutocapitalization(.never)
                    TextField("IP address", text: $lease.ip)
                        .keyboardType(.decimalPad)
                    LabeledContent("MAC address", value: lease.mac)
                } footer: {
                    Text("This device will always get this address, and be reachable by name on the local network.")
                }
                if let e = store.lastError {
                    Section { Text(e).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Reserve Address")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        let l = lease
                        Task {
                            let ok = await store.update { c in
                                c.dhcp.staticLeases.removeAll { $0.mac.lowercased() == l.mac.lowercased() }
                                c.dhcp.staticLeases.append(l)
                            }
                            if ok { dismiss() }
                        }
                    }
                    .disabled(store.busy)
                }
            }
            .onAppear { store.lastError = nil }
        }
        .presentationDetents([.medium])
    }
}
