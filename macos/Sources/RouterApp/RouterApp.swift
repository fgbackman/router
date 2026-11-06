import AppKit
import SwiftUI

@main
struct RouterApp: App {
    @StateObject private var store = RouterStore()

    init() {
        // Launched as a bare executable (swift run), make sure we get a Dock icon and windows.
        NSApplication.shared.setActivationPolicy(.regular)
    }

    var body: some Scene {
        Window("Router", id: "main") {
            ContentView()
                .environmentObject(store)
                .frame(minWidth: 860, minHeight: 540)
        }
        .commands {
            CommandGroup(after: .appInfo) {
                Button("Refresh") { Task { await store.refresh(); await store.reloadConfig() } }
                    .keyboardShortcut("r")
            }
        }

        MenuBarExtra {
            MenuBarView().environmentObject(store)
        } label: {
            Image(systemName: store.isOnline ? "network" : "network.slash")
        }
        .menuBarExtraStyle(.window)

        Settings {
            ConnectionSettingsView()
                .environmentObject(store)
                .frame(width: 460)
                .padding()
        }
    }
}
