import AppKit
import SwiftUI

struct StatusView: View {
    @EnvironmentObject var store: RouterStore

    var body: some View {
        ScrollView {
            if let s = store.status {
                VStack(alignment: .leading, spacing: 20) {
                    ForEach(s.errors, id: \.self) { e in
                        Label(e, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                            .textSelection(.enabled)
                    }

                    GroupBox("Router") {
                        Grid(alignment: .leading, horizontalSpacing: 24, verticalSpacing: 8) {
                            row("Host", "\(s.hostname) · \(s.version)")
                            row("Internet (WAN)", "\(s.wanInterface)  \(s.wanAddresses.joined(separator: ", "))")
                            row("Local network (LAN)", "\(s.lanInterface)  \(s.lanAddress)")
                            row("IP forwarding", s.forwarding == "1" ? "On" : "Off")
                            row("DNS upstreams", s.upstreams.joined(separator: ", "))
                            if let started = s.started {
                                GridRow {
                                    Text("Running since").foregroundStyle(.secondary)
                                    Text(started, style: .relative) + Text(" ago")
                                }
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(6)
                    }

                    GroupBox("DNS") {
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 130), alignment: .leading)], spacing: 16) {
                            Stat(title: "Queries", value: s.dnsQueries)
                            Stat(title: "Blocked", value: s.dnsBlocked)
                            Stat(title: "Answered locally", value: s.dnsLocal)
                            Stat(title: "Forwarded", value: s.dnsForwarded)
                            Stat(title: "Failed", value: s.dnsFailed)
                            Stat(title: "On blocklist", value: UInt64(s.blocklistSize))
                        }
                        .padding(6)
                    }

                    GroupBox("Clients") {
                        HStack {
                            Text("\(s.leases.count) device\(s.leases.count == 1 ? "" : "s") on the network")
                            Spacer()
                        }
                        .padding(6)
                    }
                }
                .padding(20)
            }
        }
    }

    private func row(_ k: String, _ v: String) -> some View {
        GridRow {
            Text(k).foregroundStyle(.secondary)
            Text(v.isEmpty ? "—" : v).textSelection(.enabled).font(.body.monospacedDigit())
        }
    }
}

struct Stat: View {
    var title: String
    var value: UInt64

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(value.formatted()).font(.title2.weight(.semibold).monospacedDigit())
        }
    }
}

struct MenuBarView: View {
    @EnvironmentObject var store: RouterStore
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Router").font(.headline)
                Spacer()
                ConnectionBadge()
            }
            if store.isOnline, let s = store.status {
                Divider()
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                    GridRow { Text("Clients").foregroundStyle(.secondary); Text("\(s.leases.count)") }
                    GridRow { Text("DNS queries").foregroundStyle(.secondary); Text(s.dnsQueries.formatted()) }
                    GridRow { Text("Blocked").foregroundStyle(.secondary); Text(s.dnsBlocked.formatted()) }
                    GridRow { Text("LAN").foregroundStyle(.secondary); Text("\(s.lanInterface) \(s.lanAddress)") }
                }
                .font(.callout)
                if !s.errors.isEmpty {
                    Label("\(s.errors.count) problem\(s.errors.count == 1 ? "" : "s")", systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange).font(.callout)
                }
            }
            Divider()
            Button("Open Router…") {
                openWindow(id: "main")
                NSApp.activate(ignoringOtherApps: true)
            }
            if store.isOnline {
                Button("Stop Router") { store.stopService() }
                Button("Restart Router") { store.restartService() }
            } else if ServiceControl.serviceInstalled {
                Button("Start Router") { store.startService() }
            }
            Divider()
            Button("Quit") { NSApp.terminate(nil) }
        }
        .buttonStyle(.borderless)
        .padding(14)
        .frame(width: 260)
    }
}

struct ConnectionSettingsView: View {
    @EnvironmentObject var store: RouterStore
    @State private var url = ""
    @State private var user = ""
    @State private var password = ""
    @State private var saved = false

    var body: some View {
        Form {
            Section {
                TextField("Router address", text: $url, prompt: Text("http://127.0.0.1:8080"))
                TextField("User", text: $user)
                SecureField("Password", text: $password)
            } footer: {
                Text("The password is stored in your login keychain. The address must match `web.listen` in the router config.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            HStack {
                Spacer()
                if saved { Text("Saved").foregroundStyle(.secondary) }
                Button("Save") {
                    store.urlString = url.trimmingCharacters(in: .whitespaces)
                    store.user = user.trimmingCharacters(in: .whitespaces)
                    if !password.isEmpty { store.savePassword(password) }
                    store.config = nil
                    saved = true
                    Task { await store.refresh() }
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .onAppear {
            url = store.urlString
            user = store.user
            password = Keychain.password(for: store.user) ?? ""
        }
    }
}
