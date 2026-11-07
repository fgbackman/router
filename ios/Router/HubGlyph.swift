import SwiftUI

/// Colours from the app icon.
enum Brand {
    static let night = Color(red: 0.06, green: 0.09, blue: 0.25)
    static let indigo = Color(red: 0.11, green: 0.16, blue: 0.42)
    static let cyan = Color(red: 0.49, green: 0.95, blue: 1.0)
    static let blue = Color(red: 0.35, green: 0.49, blue: 1.0)

    static let background = LinearGradient(colors: [indigo, night], startPoint: .top, endPoint: .bottom)
    static let beam = LinearGradient(colors: [cyan, blue], startPoint: .topLeading, endPoint: .bottomTrailing)
}

/// The app icon's network: an uplink into a hub that fans out to three
/// devices. When `active`, a halo pulses around the hub.
struct HubGlyph: View {
    var active: Bool
    var devices: Int = 3

    @State private var pulse = false

    var body: some View {
        GeometryReader { geo in
            let s = min(geo.size.width, geo.size.height)
            let hub = CGPoint(x: s * 0.5, y: s * 0.5)
            let nodes = [
                CGPoint(x: s * 0.17, y: s * 0.79),
                CGPoint(x: s * 0.5, y: s * 0.91),
                CGPoint(x: s * 0.83, y: s * 0.79),
            ]
            ZStack {
                // Halo
                Circle()
                    .fill(Brand.cyan.opacity(active ? 0.35 : 0))
                    .frame(width: s * 0.36, height: s * 0.36)
                    .scaleEffect(pulse ? 1.6 : 1.0)
                    .opacity(pulse ? 0 : 1)
                    .position(hub)

                // Uplink and routes
                Path { p in
                    p.move(to: CGPoint(x: hub.x, y: s * 0.06))
                    p.addLine(to: hub)
                    p.move(to: CGPoint(x: hub.x - s * 0.08, y: s * 0.14))
                    p.addLine(to: CGPoint(x: hub.x, y: s * 0.05))
                    p.addLine(to: CGPoint(x: hub.x + s * 0.08, y: s * 0.14))
                    for (i, n) in nodes.enumerated() {
                        p.move(to: hub)
                        if i == 1 {
                            p.addLine(to: n)
                        } else {
                            let c = CGPoint(x: (hub.x + n.x) / 2, y: hub.y + s * 0.04)
                            p.addQuadCurve(to: n, control: c)
                        }
                    }
                }
                .stroke(Brand.beam, style: StrokeStyle(lineWidth: s * 0.035, lineCap: .round, lineJoin: .round))
                .opacity(active ? 1 : 0.35)
                .shadow(color: Brand.cyan.opacity(active ? 0.7 : 0), radius: s * 0.03)

                ForEach(0..<3, id: \.self) { i in
                    let lit = active && i < max(devices, 0)
                    Circle()
                        .strokeBorder(Brand.cyan, lineWidth: s * 0.025)
                        .background(Circle().fill(Brand.night))
                        .overlay(Circle().fill(Brand.cyan.opacity(lit ? 1 : 0.3)).padding(s * 0.035))
                        .frame(width: s * 0.13, height: s * 0.13)
                        .shadow(color: Brand.cyan.opacity(lit ? 0.8 : 0), radius: s * 0.03)
                        .position(nodes[i])
                }

                // Hub
                Circle()
                    .fill(Brand.night)
                    .overlay(Circle().strokeBorder(Brand.cyan.opacity(active ? 1 : 0.5), lineWidth: s * 0.015))
                    .overlay(
                        Circle()
                            .fill(RadialGradient(colors: [.white, Brand.cyan, Color(red: 0.18, green: 0.72, blue: 1)],
                                                 center: UnitPoint(x: 0.4, y: 0.35), startRadius: 0, endRadius: s * 0.12))
                            .padding(s * 0.05)
                            .opacity(active ? 1 : 0.35)
                    )
                    .frame(width: s * 0.32, height: s * 0.32)
                    .shadow(color: Brand.cyan.opacity(active ? 0.8 : 0), radius: s * 0.05)
                    .position(hub)
            }
            .frame(width: s, height: s)
        }
        .aspectRatio(1, contentMode: .fit)
        .onAppear { startPulse() }
        .onChange(of: active) { _, _ in startPulse() }
        .accessibilityHidden(true)
    }

    private func startPulse() {
        pulse = false
        guard active else { return }
        withAnimation(.easeOut(duration: 2.2).repeatForever(autoreverses: false)) {
            pulse = true
        }
    }
}
