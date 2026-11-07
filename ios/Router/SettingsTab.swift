import SwiftUI

struct SettingsTab: View {
    @EnvironmentObject var store: PhoneStore

    var body: some View {
        NavigationStack {
            Form {
                if store.isOnline, let c = store.config {
                    Section {
                        Toggle("Block traffic from the internet", isOn: firewallBinding(\.blockInbound))
                        Toggle("Answer ping", isOn: firewallBinding(\.allowPing))
                            .disabled(!c.firewall.blockInbound)
                    } header: {
                        Text("Firewall")
                    } footer: {
                        Text("Replies to connections your devices start are always allowed.")
                    }

                    Section("DNS") {
                        Toggle("Local DNS server", isOn: dnsEnabledBinding)
                        NavigationLink {
                            BlockedDomainsView()
                        } label: {
                            LabeledContent("Blocked domains", value: "\(c.dns.blockedDomains.count)")
                        }
                        LabeledContent("Blocklists", value: "\(c.dns.blocklists.count)")
                        LabeledContent("Local domain", value: c.dns.domain)
                    }

                    Section("Network") {
                        LabeledContent("Internet port", value: c.wan.interface)
                        LabeledContent("Local port", value: c.lan.interface)
                        LabeledContent("Router address", value: c.lan.address)
                        LabeledContent("DHCP range", value: "\(lastOctet(c.dhcp.rangeStart))–\(lastOctet(c.dhcp.rangeEnd))")
                    }

                    if let e = store.lastError {
                        Section { Text(e).foregroundStyle(.red) }
                    }
                }

                Section {
                    NavigationLink("Connection") { ConnectView().navigationTitle("Connection") }
                    LabeledContent("Address", value: store.urlString)
                    LabeledContent("Status", value: statusText)
                } header: {
                    Text("This app")
                } footer: {
                    Text("Starting and stopping the router, and changing its ports, is done in the Mac app.")
                }
            }
            .navigationTitle("Settings")
            .disabled(store.busy)
        }
    }

    private var statusText: String {
        switch store.connection {
        case .online: return "Connected"
        case .connecting: return "Connecting…"
        case .offline: return "Not reachable"
        case .unauthorized: return "Wrong password"
        case .locked: return "No password set"
        }
    }

    private func lastOctet(_ ip: String) -> String {
        ip.split(separator: ".").count == 4 ? ".\(ip.split(separator: ".").last!)" : ip
    }

    /// A toggle that applies immediately.
    private func firewallBinding(_ kp: WritableKeyPath<RouterConfig.Firewall, Bool>) -> Binding<Bool> {
        Binding(
            get: { store.config?.firewall[keyPath: kp] ?? false },
            set: { v in Task { await store.update { $0.firewall[keyPath: kp] = v } } }
        )
    }

    private var dnsEnabledBinding: Binding<Bool> {
        Binding(
            get: { store.config?.dns.enabled ?? false },
            set: { v in Task { await store.update { $0.dns.enabled = v } } }
        )
    }
}

struct BlockedDomainsView: View {
    @EnvironmentObject var store: PhoneStore
    @State private var newDomain = ""

    var body: some View {
        List {
            Section {
                HStack {
                    TextField("ads.example.com", text: $newDomain)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .onSubmit(add)
                    Button("Block", action: add)
                        .disabled(newDomain.trimmingCharacters(in: .whitespaces).isEmpty || store.busy)
                }
            } footer: {
                Text("Subdomains are blocked too.")
            }
            Section {
                ForEach(store.config?.dns.blockedDomains ?? [], id: \.self) { d in
                    Text(d).font(.body.monospaced())
                }
                .onDelete { idx in
                    let doomed = idx.compactMap { store.config?.dns.blockedDomains[$0] }
                    Task { await store.update { c in c.dns.blockedDomains.removeAll { doomed.contains($0) } } }
                }
            }
            if let e = store.lastError {
                Section { Text(e).foregroundStyle(.red) }
            }
        }
        .navigationTitle("Blocked Domains")
    }

    private func add() {
        let d = newDomain.trimmingCharacters(in: .whitespaces).lowercased()
        guard !d.isEmpty else { return }
        Task {
            if await store.update({ c in
                if !c.dns.blockedDomains.contains(d) { c.dns.blockedDomains.append(d) }
            }) {
                newDomain = ""
            }
        }
    }
}
