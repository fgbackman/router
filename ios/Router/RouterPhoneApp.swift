import SwiftUI

@main
struct RouterPhoneApp: App {
    @StateObject private var store = PhoneStore()
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .tint(Brand.blue)
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                store.startPolling()
            } else {
                store.stopPolling()
            }
        }
    }
}

enum Tab: String {
    case status, clients, forwarding, settings
}

struct RootView: View {
    @EnvironmentObject var store: PhoneStore
    @State private var tab: Tab = Tab(rawValue: ProcessInfo.processInfo.environment["ROUTER_TAB"] ?? "") ?? .status

    var body: some View {
        TabView(selection: $tab) {
            StatusTab()
                .tabItem { Label("Status", systemImage: "gauge.with.dots.needle.67percent") }
                .tag(Tab.status)
            ClientsTab()
                .tabItem { Label("Devices", systemImage: "laptopcomputer.and.iphone") }
                .tag(Tab.clients)
            ForwardsTab()
                .tabItem { Label("Ports", systemImage: "arrow.left.arrow.right") }
                .tag(Tab.forwarding)
            SettingsTab()
                .tabItem { Label("Settings", systemImage: "gearshape") }
                .tag(Tab.settings)
        }
        .task { store.startPolling() }
    }
}

/// Replaces a tab's content while the router can't be reached.
struct NeedsConnection<Content: View>: View {
    @EnvironmentObject var store: PhoneStore
    @ViewBuilder var content: () -> Content

    var body: some View {
        if store.isOnline, store.config != nil {
            content()
        } else {
            ConnectView()
        }
    }
}

/// Explains why the router isn't reachable and lets you fix the connection.
struct ConnectView: View {
    @EnvironmentObject var store: PhoneStore
    @State private var url = ""
    @State private var user = ""
    @State private var password = ""

    var body: some View {
        Form {
            Section {
                VStack(spacing: 14) {
                    HubGlyph(active: false)
                        .frame(width: 110, height: 110)
                    Text(title).font(.title3.bold())
                    Text(message)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                    if store.connection == .connecting {
                        ProgressView()
                    }
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, 8)
            }
            Section {
                TextField("Router address", text: $url)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                TextField("User", text: $user)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                SecureField("Admin password", text: $password)
                Button("Connect") {
                    store.saveConnection(url: url, user: user, password: password)
                }
                .disabled(url.isEmpty || user.isEmpty)
            } header: {
                Text("Connection")
            } footer: {
                Text("Use the router's LAN address (usually http://192.168.50.1:8080) while your phone is on the router's network. In the Mac app, turn on Network › Allow devices on the local network.")
            }
        }
        .onAppear {
            url = store.urlString
            user = store.user
        }
    }

    private var title: String {
        switch store.connection {
        case .connecting: return "Connecting…"
        case .unauthorized: return "Wrong password"
        case .locked: return "No admin password set"
        case .offline: return "Can't reach the router"
        case .online: return "Loading…"
        }
    }

    private var message: String {
        switch store.connection {
        case .connecting: return store.urlString
        case .unauthorized: return "The router rejected the user name or password."
        case .locked: return "Set one on the Mac with: sudo router passwd"
        case .offline(let why): return "Make sure your phone is on the router's network.\n\(why)"
        case .online: return ""
        }
    }
}
