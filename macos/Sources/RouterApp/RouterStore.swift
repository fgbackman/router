import Foundation
import SwiftUI

enum ConnectionState: Equatable {
    case connecting
    case online
    case offline(String)
    case unauthorized
    case locked
}

/// App-wide state: connection settings, the latest status and config, and
/// actions that change them.
@MainActor
final class RouterStore: ObservableObject {
    @Published var status: RouterStatus?
    @Published var config: RouterConfig?
    @Published var connection: ConnectionState = .connecting
    @Published var lastError: String?
    @Published var busy = false
    @Published var showSetup = false

    @AppStorage("routerURL") var urlString = "http://127.0.0.1:8080"
    @AppStorage("routerUser") var user = "admin"

    private var pollTask: Task<Void, Never>?

    init() {
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    var isOnline: Bool { connection == .online }

    /// Read from the keychain once per user name; reading it on every poll
    /// could trigger repeated keychain prompts.
    private var cachedPassword: (user: String, password: String)?

    private var password: String {
        // For automated UI checks (CI), which can't answer keychain prompts.
        if let p = ProcessInfo.processInfo.environment["ROUTER_APP_PASSWORD"] { return p }
        if let c = cachedPassword, c.user == user { return c.password }
        let p = Keychain.password(for: user) ?? ""
        cachedPassword = (user, p)
        return p
    }

    var client: APIClient? {
        guard let url = URL(string: urlString) else { return nil }
        return APIClient(baseURL: url, user: user, password: password)
    }

    func refresh() async {
        guard let client else {
            connection = .offline("The router address in Settings isn't a valid URL.")
            return
        }
        do {
            let s = try await client.status()
            status = s
            if config == nil {
                config = try await client.config()
            }
            connection = .online
        } catch APIError.unauthorized {
            connection = .unauthorized
        } catch APIError.locked {
            connection = .locked
        } catch {
            connection = .offline(error.localizedDescription)
        }
    }

    func reloadConfig() async {
        guard let client else { return }
        do {
            config = try await client.config()
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// Edits a copy of the config and sends it. Returns whether it was applied.
    @discardableResult
    func update(_ edit: (inout RouterConfig) -> Void) async -> Bool {
        guard let client, var c = config else { return false }
        edit(&c)
        busy = true
        defer { busy = false }
        do {
            config = try await client.save(c)
            lastError = nil
            // The daemon restarts its services after a change; give it a moment.
            try? await Task.sleep(nanoseconds: 800_000_000)
            await refresh()
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    func savePassword(_ password: String) {
        Keychain.setPassword(password, for: user)
        cachedPassword = (user, password)
        Task { await refresh() }
    }

    // MARK: Service control (prompts for the admin password)

    func startService() { runService { try ServiceControl.start() } }
    func stopService() { runService { try ServiceControl.stop() } }
    func restartService() { runService { try ServiceControl.restart() } }

    private func runService(_ action: () throws -> Void) {
        do {
            try action()
            lastError = nil
        } catch {
            if (error as? ServiceControl.Failure)?.message != "Cancelled." {
                lastError = error.localizedDescription
            }
        }
        connection = .connecting
        Task {
            try? await Task.sleep(nanoseconds: 1_500_000_000)
            await refresh()
        }
    }
}
