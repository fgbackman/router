import Foundation

// Mirrors internal/config.Config in the Go daemon. Go encodes empty slices as
// null, so array fields decode missing/null as [].

struct RouterConfig: Codable, Equatable {
    var wan: WAN
    var lan: LAN
    var dhcp: DHCP
    var dns: DNS
    var firewall: Firewall
    var web: Web
    var stateDir: String

    struct WAN: Codable, Equatable {
        var interface: String
    }

    struct LAN: Codable, Equatable {
        var interface: String
        var address: String
        var configureAddress: Bool
    }

    struct DHCP: Codable, Equatable {
        var enabled: Bool
        var rangeStart: String
        var rangeEnd: String
        var leaseTime: String
        var staticLeases: [StaticLease]

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            enabled = try c.decode(Bool.self, forKey: .enabled)
            rangeStart = try c.decode(String.self, forKey: .rangeStart)
            rangeEnd = try c.decode(String.self, forKey: .rangeEnd)
            leaseTime = try c.decode(String.self, forKey: .leaseTime)
            staticLeases = try c.decodeIfPresent([StaticLease].self, forKey: .staticLeases) ?? []
        }
    }

    struct StaticLease: Codable, Equatable, Hashable {
        var mac: String
        var ip: String
        var hostname: String
    }

    struct DNS: Codable, Equatable {
        var enabled: Bool
        var domain: String
        var upstreams: [String]
        var blocklists: [String]
        var blockedDomains: [String]
        var hosts: [HostRecord]

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            enabled = try c.decode(Bool.self, forKey: .enabled)
            domain = try c.decode(String.self, forKey: .domain)
            upstreams = try c.decodeIfPresent([String].self, forKey: .upstreams) ?? []
            blocklists = try c.decodeIfPresent([String].self, forKey: .blocklists) ?? []
            blockedDomains = try c.decodeIfPresent([String].self, forKey: .blockedDomains) ?? []
            hosts = try c.decodeIfPresent([HostRecord].self, forKey: .hosts) ?? []
        }
    }

    struct HostRecord: Codable, Equatable, Hashable {
        var name: String
        var ip: String
    }

    struct Firewall: Codable, Equatable {
        var blockInbound: Bool
        var allowPing: Bool
        var openPorts: [String]
        var portForwards: [PortForward]

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            blockInbound = try c.decode(Bool.self, forKey: .blockInbound)
            allowPing = try c.decode(Bool.self, forKey: .allowPing)
            openPorts = try c.decodeIfPresent([String].self, forKey: .openPorts) ?? []
            portForwards = try c.decodeIfPresent([PortForward].self, forKey: .portForwards) ?? []
        }
    }

    struct PortForward: Codable, Equatable, Hashable {
        var name: String
        var `protocol`: String
        var externalPort: Int
        var internalIP: String
        var internalPort: Int
    }

    struct Web: Codable, Equatable {
        var enabled: Bool
        var listen: String
        var user: String
        var passwordHash: String
    }
}

// Mirrors internal/web.Status.
struct RouterStatus: Decodable {
    var version: String
    var started: Date?
    var hostname: String
    var wanInterface: String
    var wanAddresses: [String]
    var lanInterface: String
    var lanAddress: String
    var forwarding: String
    var leases: [Lease]
    var dnsQueries: UInt64
    var dnsBlocked: UInt64
    var dnsLocal: UInt64
    var dnsForwarded: UInt64
    var dnsFailed: UInt64
    var blocklistSize: Int
    var upstreams: [String]
    var pf: String
    var errors: [String]

    enum CodingKeys: String, CodingKey {
        case version, started, hostname, wanInterface, wanAddresses, lanInterface, lanAddress
        case forwarding, leases, dnsQueries, dnsBlocked, dnsLocal, dnsForwarded, dnsFailed
        case blocklistSize, upstreams, pf, errors
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decode(String.self, forKey: .version)
        started = GoTime.parse(try c.decodeIfPresent(String.self, forKey: .started) ?? "")
        hostname = try c.decode(String.self, forKey: .hostname)
        wanInterface = try c.decode(String.self, forKey: .wanInterface)
        wanAddresses = try c.decodeIfPresent([String].self, forKey: .wanAddresses) ?? []
        lanInterface = try c.decode(String.self, forKey: .lanInterface)
        lanAddress = try c.decode(String.self, forKey: .lanAddress)
        forwarding = try c.decode(String.self, forKey: .forwarding)
        leases = try c.decodeIfPresent([Lease].self, forKey: .leases) ?? []
        dnsQueries = try c.decode(UInt64.self, forKey: .dnsQueries)
        dnsBlocked = try c.decode(UInt64.self, forKey: .dnsBlocked)
        dnsLocal = try c.decode(UInt64.self, forKey: .dnsLocal)
        dnsForwarded = try c.decode(UInt64.self, forKey: .dnsForwarded)
        dnsFailed = try c.decode(UInt64.self, forKey: .dnsFailed)
        blocklistSize = try c.decode(Int.self, forKey: .blocklistSize)
        upstreams = try c.decodeIfPresent([String].self, forKey: .upstreams) ?? []
        pf = try c.decodeIfPresent(String.self, forKey: .pf) ?? ""
        errors = try c.decodeIfPresent([String].self, forKey: .errors) ?? []
    }
}

struct Lease: Decodable, Identifiable, Hashable {
    var mac: String
    var ip: String
    var hostname: String
    var expires: Date?
    var isStatic: Bool

    var id: String { mac }

    enum CodingKeys: String, CodingKey {
        case mac, ip, hostname, expires
        case isStatic = "static"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        mac = try c.decode(String.self, forKey: .mac)
        ip = try c.decode(String.self, forKey: .ip)
        hostname = try c.decodeIfPresent(String.self, forKey: .hostname) ?? ""
        expires = GoTime.parse(try c.decodeIfPresent(String.self, forKey: .expires) ?? "")
        isStatic = try c.decodeIfPresent(Bool.self, forKey: .isStatic) ?? false
    }
}

/// Output of `router interfaces -json`.
struct NetInterface: Decodable, Identifiable, Hashable {
    var device: String
    var port: String
    var mac: String
    var up: Bool
    var addresses: [String]
    var defaultRoute: Bool

    var id: String { device }

    var label: String {
        var s = device
        if !port.isEmpty { s += " — \(port)" }
        if let a = addresses.first { s += " (\(a))" }
        if defaultRoute { s += " · internet" }
        return s
    }
}

enum GoTime {
    /// Parses Go's RFC 3339 output (up to nanosecond precision). Go's zero
    /// time (year 1) comes back as nil.
    static func parse(_ raw: String) -> Date? {
        guard !raw.isEmpty, !raw.hasPrefix("0001-") else { return nil }
        var s = raw
        if let dot = s.firstIndex(of: "."),
           let tz = s[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let frac = String(s[s.index(after: dot)..<tz].prefix(3))
            s = String(s[..<dot]) + "." + frac.padding(toLength: 3, withPad: "0", startingAt: 0) + String(s[tz...])
        }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s)
    }
}
