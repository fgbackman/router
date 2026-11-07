import Foundation
import SwiftUI

enum PhoneConnection: Equatable {
    case connecting
    case online
    case offline(String)
    case unauthorized
    case locked
}

/// App state for the iPhone: where the router is, its latest status and
/// config, and edits sent back to it.
@MainActor
final class PhoneStore: ObservableObject {
    @Published var status: RouterStatus?
    @Published var config: RouterConfig?
    @Published var connection: PhoneConnection = .connecting
    @Published var lastError: String?
    @Published var busy = false

    @AppStorage("routerURL") var urlString = "http://192.168.50.1:8080"
    @AppStorage("routerUser") var user = "admin"

    private var pollTask: Task<Void, Never>?
    private var cachedPassword: (user: String, password: String)?

    init() {
        // Overrides for automated screenshots in CI.
        let env = ProcessInfo.processInfo.environment
        if let url = env["ROUTER_URL"] { urlString = url }
    }

    var isOnline: Bool { connection == .online }

    var password: String {
        if let p = ProcessInfo.processInfo.environment["ROUTER_APP_PASSWORD"] { return p }
        if let c = cachedPassword, c.user == user { return c.password }
        let p = Keychain.password(for: user) ?? ""
        cachedPassword = (user, p)
        return p
    }

    var hasPassword: Bool { !password.isEmpty }

    private var client: APIClient? {
        guard let url = URL(string: urlString), url.scheme != nil else { return nil }
        return APIClient(baseURL: url, user: user, password: password)
    }

    /// Polls every few seconds while the app is in the foreground.
    func startPolling() {
        guard pollTask == nil else { return }
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(nanoseconds: 3_000_000_000)
            }
        }
    }

    func stopPolling() {
        pollTask?.cancel()
        pollTask = nil
    }

    func refresh() async {
        guard let client else {
            connection = .offline("The router address isn't a valid URL.")
            return
        }
        do {
            status = try await client.status()
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

    func reloadAll() async {
        config = nil
        await refresh()
    }

    @discardableResult
    func update(_ edit: (inout RouterConfig) -> Void) async -> Bool {
        guard let client, var c = config else { return false }
        edit(&c)
        busy = true
        defer { busy = false }
        do {
            config = try await client.save(c)
            lastError = nil
            try? await Task.sleep(nanoseconds: 800_000_000)
            await refresh()
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    func saveConnection(url: String, user: String, password: String) {
        urlString = url.trimmingCharacters(in: .whitespaces)
        self.user = user.trimmingCharacters(in: .whitespaces)
        if !password.isEmpty {
            Keychain.setPassword(password, for: self.user)
            cachedPassword = (self.user, password)
        }
        connection = .connecting
        Task { await reloadAll() }
    }
}
