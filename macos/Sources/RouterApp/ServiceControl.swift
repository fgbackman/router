import AppKit
import Foundation

/// Drives the `router` command-line tool. Anything that needs root goes
/// through AppleScript's `with administrator privileges`, which shows the
/// standard macOS password prompt.
enum ServiceControl {
    static let cliPath = "/usr/local/bin/router"
    static let plistPath = "/Library/LaunchDaemons/com.fgbackman.router.plist"
    static let configPath = "/usr/local/etc/router/config.json"

    static var cliInstalled: Bool { FileManager.default.isExecutableFile(atPath: cliPath) }
    static var serviceInstalled: Bool { FileManager.default.fileExists(atPath: plistPath) }
    static var configExists: Bool { FileManager.default.fileExists(atPath: configPath) }

    struct Failure: LocalizedError {
        var message: String
        var errorDescription: String? { message }
    }

    /// Lists network interfaces (no root needed).
    static func interfaces() throws -> [NetInterface] {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: cliPath)
        p.arguments = ["interfaces", "-json"]
        let out = Pipe()
        let err = Pipe()
        p.standardOutput = out
        p.standardError = err
        try p.run()
        let data = out.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        guard p.terminationStatus == 0 else {
            let msg = String(data: err.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
            throw Failure(message: msg.isEmpty ? "router interfaces failed" : msg)
        }
        return try JSONDecoder().decode([NetInterface].self, from: data)
    }

    static func start() throws { _ = try runAsAdmin("\(q(cliPath)) start") }
    static func stop() throws { _ = try runAsAdmin("\(q(cliPath)) stop") }
    static func restart() throws { _ = try runAsAdmin("\(q(cliPath)) stop; \(q(cliPath)) start") }

    /// First-run setup: write the config, set the password, install the LaunchDaemon.
    static func setUp(wan: String, lan: String, address: String, password: String) throws {
        // Hand the password over in a private temp file rather than on the
        // command line, where other processes could see it.
        let dir = FileManager.default.temporaryDirectory
        let file = dir.appendingPathComponent("router-pw-\(UUID().uuidString)")
        FileManager.default.createFile(atPath: file.path, contents: Data(password.utf8),
                                       attributes: [.posixPermissions: 0o600])
        defer { try? FileManager.default.removeItem(at: file) }
        let cli = q(cliPath)
        let script = [
            "\(cli) init -force -wan \(q(wan)) -lan \(q(lan)) -addr \(q(address))",
            "\(cli) passwd -stdin < \(q(file.path))",
            "\(cli) install",
        ].joined(separator: " && ")
        _ = try runAsAdmin(script)
    }

    /// Shell-quotes one argument.
    static func q(_ s: String) -> String {
        "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
    }

    @discardableResult
    static func runAsAdmin(_ shell: String) throws -> String {
        let escaped = shell
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        let source = "do shell script \"\(escaped)\" with administrator privileges"
        guard let script = NSAppleScript(source: source) else {
            throw Failure(message: "Couldn't prepare the administrator command.")
        }
        var info: NSDictionary?
        let result = script.executeAndReturnError(&info)
        if let info {
            let code = info[NSAppleScript.errorNumber] as? Int ?? 0
            if code == -128 { throw Failure(message: "Cancelled.") }
            let msg = info[NSAppleScript.errorMessage] as? String ?? "Unknown error"
            throw Failure(message: msg)
        }
        return result.stringValue ?? ""
    }
}
