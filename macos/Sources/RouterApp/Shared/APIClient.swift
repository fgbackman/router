import Foundation

// Shared by the Mac app (macos/) and the iPhone app (ios/).
import Security

enum APIError: LocalizedError {
    case unauthorized
    case locked
    case server(String)
    case transport(String)

    var errorDescription: String? {
        switch self {
        case .unauthorized: return "The router rejected the user name or password."
        case .locked: return "The router has no admin password yet."
        case .server(let m): return m
        case .transport(let m): return m
        }
    }
}

/// Talks to the daemon's JSON API (internal/web, /api/*).
struct APIClient {
    var baseURL: URL
    var user: String
    var password: String

    private static let session: URLSession = {
        let c = URLSessionConfiguration.ephemeral
        c.timeoutIntervalForRequest = 5
        c.httpCookieStorage = nil
        c.urlCredentialStorage = nil
        return URLSession(configuration: c)
    }()

    func status() async throws -> RouterStatus {
        try await send("GET", "api/status")
    }

    func config() async throws -> RouterConfig {
        try await send("GET", "api/config")
    }

    func save(_ config: RouterConfig) async throws -> RouterConfig {
        try await send("PUT", "api/config", body: JSONEncoder().encode(config))
    }

    private func send<T: Decodable>(_ method: String, _ path: String, body: Data? = nil) async throws -> T {
        var req = URLRequest(url: baseURL.appendingPathComponent(path))
        req.httpMethod = method
        let token = Data("\(user):\(password)".utf8).base64EncodedString()
        req.setValue("Basic \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("RouterApp", forHTTPHeaderField: "X-Router-Client")
        if let body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let result: (Data, URLResponse)
        do {
            result = try await Self.session.data(for: req)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        let (data, resp) = result
        let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
        switch code {
        case 200:
            do {
                return try JSONDecoder().decode(T.self, from: data)
            } catch {
                throw APIError.server("Unexpected reply from the router: \(error)")
            }
        case 401:
            throw APIError.unauthorized
        case 503:
            throw APIError.locked
        default:
            if let b = try? JSONDecoder().decode(ErrorBody.self, from: data) {
                throw APIError.server(b.error)
            }
            let text = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            throw APIError.server("HTTP \(code): \(text)")
        }
    }
}

private struct ErrorBody: Decodable {
    var error: String
}

/// Stores the admin password in the login keychain.
enum Keychain {
    private static let service = "com.fgbackman.router.app"

    static func password(for account: String) -> String? {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let d = out as? Data else {
            return nil
        }
        return String(data: d, encoding: .utf8)
    }

    @discardableResult
    static func setPassword(_ password: String, for account: String) -> Bool {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(q as CFDictionary)
        var add = q
        add[kSecValueData as String] = Data(password.utf8)
        add[kSecAttrLabel as String] = "Router admin password"
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }
}
