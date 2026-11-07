import SwiftUI

struct StatusTab: View {
    @EnvironmentObject var store: PhoneStore

    var body: some View {
        NavigationStack {
            NeedsConnection {
                ScrollView {
                    if let s = store.status {
                        VStack(spacing: 16) {
                            HeroCard(status: s)
                            ForEach(s.errors, id: \.self) { e in
                                Label(e, systemImage: "exclamationmark.triangle.fill")
                                    .font(.callout)
                                    .foregroundStyle(.orange)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .padding(12)
                                    .background(.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 14))
                            }
                            DNSCard(status: s)
                            NetworkCard(status: s)
                        }
                        .padding(16)
                    }
                }
                .background(Color(.systemGroupedBackground))
                .refreshable { await store.reloadAll() }
            }
            .navigationTitle("Router")
        }
    }
}

struct HeroCard: View {
    var status: RouterStatus

    var body: some View {
        HStack(spacing: 18) {
            HubGlyph(active: true, devices: status.leases.count)
                .frame(width: 112, height: 112)
            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 6) {
                    Circle().fill(status.errors.isEmpty ? .green : .orange).frame(width: 8, height: 8)
                    Text(status.errors.isEmpty ? "Online" : "Needs attention")
                        .font(.subheadline.weight(.semibold))
                }
                .foregroundStyle(.white)
                Text(status.hostname)
                    .font(.title3.bold())
                    .foregroundStyle(.white)
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
                Text("\(status.leases.count) device\(status.leases.count == 1 ? "" : "s") connected")
                    .font(.subheadline)
                    .foregroundStyle(Brand.cyan)
                if let started = status.started {
                    (Text("Up ") + Text(started, style: .relative))
                        .font(.caption)
                        .foregroundStyle(.white.opacity(0.6))
                }
            }
            Spacer(minLength: 0)
        }
        .padding(18)
        .background(
            ZStack {
                Brand.background
                RadialGradient(colors: [Brand.cyan.opacity(0.25), .clear], center: .leading, startRadius: 10, endRadius: 220)
            },
            in: RoundedRectangle(cornerRadius: 22, style: .continuous)
        )
        .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous).strokeBorder(.white.opacity(0.1)))
        .shadow(color: Brand.night.opacity(0.3), radius: 12, y: 6)
    }
}

struct DNSCard: View {
    var status: RouterStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("DNS").font(.headline)
            let blockedShare = status.dnsQueries > 0 ? Double(status.dnsBlocked) / Double(status.dnsQueries) : 0
            HStack(alignment: .firstTextBaseline) {
                Text(status.dnsBlocked.formatted())
                    .font(.system(size: 34, weight: .bold, design: .rounded))
                Text("blocked").foregroundStyle(.secondary)
                Spacer()
                Text(blockedShare.formatted(.percent.precision(.fractionLength(0))))
                    .font(.headline)
                    .foregroundStyle(Brand.blue)
            }
            ProgressView(value: blockedShare)
                .tint(Brand.blue)
            Grid(alignment: .leading, horizontalSpacing: 18, verticalSpacing: 6) {
                GridRow {
                    Metric(title: "Queries", value: status.dnsQueries)
                    Metric(title: "Forwarded", value: status.dnsForwarded)
                }
                GridRow {
                    Metric(title: "Local", value: status.dnsLocal)
                    Metric(title: "On blocklist", value: UInt64(status.blocklistSize))
                }
            }
        }
        .padding(16)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }
}

struct Metric: View {
    var title: String
    var value: UInt64

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(value.formatted()).font(.body.weight(.semibold).monospacedDigit())
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct NetworkCard: View {
    var status: RouterStatus

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Network").font(.headline)
            row("Internet", "\(status.wanInterface) · \(status.wanAddresses.first ?? "no address")", "globe")
            Divider()
            row("Local network", "\(status.lanInterface) · \(status.lanAddress)", "house")
            Divider()
            row("DNS upstream", status.upstreams.first ?? "—", "arrow.up.forward.circle")
            Divider()
            row("Version", status.version, "info.circle")
        }
        .padding(16)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private func row(_ title: String, _ value: String, _ icon: String) -> some View {
        HStack {
            Label(title, systemImage: icon).foregroundStyle(.secondary)
            Spacer()
            Text(value).font(.callout.monospacedDigit()).multilineTextAlignment(.trailing)
        }
        .font(.callout)
    }
}
