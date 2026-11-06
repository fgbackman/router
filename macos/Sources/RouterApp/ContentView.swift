import AppKit
import SwiftUI

enum Pane: String, CaseIterable, Identifiable {
    case status = "Status"
    case clients = "Clients"
    case forwarding = "Port Forwarding"
    case firewall = "Firewall"
    case dns = "DNS"
    case network = "Network"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .status: return "gauge"
        case .clients: return "laptopcomputer.and.iphone"
        case .forwarding: return "arrow.left.arrow.right"
        case .firewall: return "shield.lefthalf.filled"
        case .dns: return "globe"
        case .network: return "point.3.connected.trianglepath.dotted"
        }
    }
}

struct ContentView: View {
    @EnvironmentObject var store: RouterStore
    @State private var pane: Pane? = .status

    var body: some View {
        NavigationSplitView {
            List(Pane.allCases, selection: $pane) { p in
                Label(p.rawValue, systemImage: p.icon).tag(p)
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 200)
        } detail: {
            VStack(spacing: 0) {
                if let err = store.lastError {
                    ErrorBanner(message: err) { store.lastError = nil }
                }
                if store.isOnline, store.config != nil {
                    detail
                } else {
                    OfflineView()
                }
            }
            .navigationTitle(pane?.rawValue ?? "Router")
        }
        .toolbar {
            ToolbarItemGroup {
                ConnectionBadge()
                Menu {
                    Button("Start Router") { store.startService() }
                    Button("Stop Router") { store.stopService() }
                    Button("Restart Router") { store.restartService() }
                    Divider()
                    Button("Set Up Router…") { store.showSetup = true }
                } label: {
                    Label("Service", systemImage: "power")
                }
                Button {
                    Task { await store.refresh(); await store.reloadConfig() }
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
        }
        .sheet(isPresented: $store.showSetup) {
            SetupSheet().environmentObject(store)
        }
    }

    @ViewBuilder private var detail: some View {
        switch pane ?? .status {
        case .status: StatusView()
        case .clients: ClientsView()
        case .forwarding: PortForwardsView()
        case .firewall: FirewallView()
        case .dns: DNSView()
        case .network: NetworkView()
        }
    }
}

struct ConnectionBadge: View {
    @EnvironmentObject var store: RouterStore

    var body: some View {
        HStack(spacing: 6) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(text).font(.callout).foregroundStyle(.secondary)
        }
        .help(text)
    }

    private var color: Color {
        switch store.connection {
        case .online: return .green
        case .connecting: return .yellow
        default: return .red
        }
    }

    private var text: String {
        switch store.connection {
        case .online: return "Running"
        case .connecting: return "Connecting…"
        case .offline: return "Not running"
        case .unauthorized: return "Wrong password"
        case .locked: return "No password set"
        }
    }
}

struct ErrorBanner: View {
    var message: String
    var dismiss: () -> Void

    var body: some View {
        HStack(alignment: .top) {
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
            Text(message).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
            Button(action: dismiss) { Image(systemName: "xmark") }.buttonStyle(.borderless)
        }
        .padding(10)
        .background(Color.red.opacity(0.1))
    }
}

/// Shown when the app can't talk to the router: explains why and offers the fix.
struct OfflineView: View {
    @EnvironmentObject var store: RouterStore

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: icon).font(.system(size: 44)).foregroundStyle(.secondary)
            Text(title).font(.title2.bold())
            Text(message)
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .frame(maxWidth: 460)
                .textSelection(.enabled)
            HStack {
                actions
            }
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var icon: String {
        switch store.connection {
        case .unauthorized, .locked: return "lock"
        case .connecting: return "hourglass"
        default: return ServiceControl.cliInstalled ? "network.slash" : "shippingbox"
        }
    }

    private var title: String {
        if !ServiceControl.cliInstalled { return "Install the router tool" }
        switch store.connection {
        case .connecting: return "Connecting…"
        case .unauthorized: return "Enter the admin password"
        case .locked: return "Set an admin password"
        default:
            return ServiceControl.serviceInstalled ? "The router isn't running" : "Set up your router"
        }
    }

    private var message: String {
        if !ServiceControl.cliInstalled {
            return "This app controls the `router` command-line tool, which isn't in /usr/local/bin yet. Build it with `make universal && sudo make install` in the router folder, then come back."
        }
        switch store.connection {
        case .connecting: return "Talking to \(store.urlString)"
        case .unauthorized: return "The router is running but rejected the saved password. Enter the admin password you chose with `router passwd`."
        case .locked: return "The router is running, but its admin interface stays locked until you set a password with `sudo router passwd`."
        case .offline(let why):
            if ServiceControl.serviceInstalled {
                return "Couldn't reach \(store.urlString). Start the router, or check the address in Settings.\n\n\(why)"
            }
            return "Choose which port connects to the internet and which serves your devices. The app writes the config, sets a password and installs the router so it starts at boot."
        case .online: return ""
        }
    }

    @ViewBuilder private var actions: some View {
        if ServiceControl.cliInstalled {
            switch store.connection {
            case .unauthorized, .locked:
                Button("Open Settings…") { openSettings() }.keyboardShortcut(.defaultAction)
            case .offline:
                if ServiceControl.serviceInstalled {
                    Button("Start Router") { store.startService() }.keyboardShortcut(.defaultAction)
                    Button("Set Up Again…") { store.showSetup = true }
                } else {
                    Button("Set Up Router…") { store.showSetup = true }.keyboardShortcut(.defaultAction)
                }
                Button("Settings…") { openSettings() }
            default:
                EmptyView()
            }
        }
    }

    private func openSettings() {
        openSettingsWindow()
    }
}

/// Opens the app's Settings window (works on macOS 13 and later).
@MainActor
func openSettingsWindow() {
    NSApp.activate(ignoringOtherApps: true)
    if !NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil) {
        NSApp.sendAction(Selector(("showPreferencesWindow:")), to: nil, from: nil)
    }
}
