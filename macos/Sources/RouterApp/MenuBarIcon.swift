import AppKit

/// The menu bar glyph: the app icon's uplink, hub and three devices, drawn as
/// a template image so macOS tints it for light and dark menu bars.
/// Geometry is on an 18×18 pt canvas with the origin at the top left.
enum MenuBarIcon {
    enum State: Hashable {
        case online      // filled hub
        case connecting  // hollow hub
        case attention   // filled hub plus a dot: the router reports problems
        case offline     // slashed
    }

    private static var cache: [State: NSImage] = [:]

    static func image(_ state: State) -> NSImage {
        if let img = cache[state] { return img }
        let img = NSImage(size: NSSize(width: 18, height: 18), flipped: true) { _ in
            draw(state)
            return true
        }
        img.isTemplate = true
        img.accessibilityDescription = description(state)
        cache[state] = img
        return img
    }

    static func description(_ state: State) -> String {
        switch state {
        case .online: return "Router running"
        case .connecting: return "Router connecting"
        case .attention: return "Router needs attention"
        case .offline: return "Router not running"
        }
    }

    private static func draw(_ state: State) {
        guard let ctx = NSGraphicsContext.current else { return }
        NSColor.black.setStroke()
        NSColor.black.setFill()
        let hub = NSPoint(x: 9, y: 9.5)
        let nodes = [NSPoint(x: 3, y: 14.5), NSPoint(x: 9, y: 16.2), NSPoint(x: 15, y: 14.5)]

        // Uplink arrow (the internet).
        let up = NSBezierPath()
        up.lineWidth = 1.6
        up.lineCapStyle = .round
        up.lineJoinStyle = .round
        up.move(to: NSPoint(x: 9, y: 6.2))
        up.line(to: NSPoint(x: 9, y: 1.7))
        up.move(to: NSPoint(x: 6.9, y: 3.7))
        up.line(to: NSPoint(x: 9, y: 1.6))
        up.line(to: NSPoint(x: 11.1, y: 3.7))
        up.stroke()

        // Routes to the devices.
        let routes = NSBezierPath()
        routes.lineWidth = 1.4
        routes.lineCapStyle = .round
        for n in nodes {
            routes.move(to: hub)
            routes.line(to: n)
        }
        routes.stroke()
        for n in nodes {
            circle(n, 1.7).fill()
        }

        // Hub: knock out the crossing lines, then ring and core.
        clear { circle(hub, 4.3).fill() }
        let ring = circle(hub, 3.3)
        ring.lineWidth = 1.4
        ring.stroke()
        if state != .connecting {
            circle(hub, 1.6).fill()
        }

        switch state {
        case .offline:
            let slash = NSBezierPath()
            slash.move(to: NSPoint(x: 2.2, y: 2.2))
            slash.line(to: NSPoint(x: 15.8, y: 15.8))
            slash.lineCapStyle = .round
            clear {
                slash.lineWidth = 3.6
                slash.stroke()
            }
            slash.lineWidth = 1.5
            slash.stroke()
        case .attention:
            let dot = NSPoint(x: 15.2, y: 3.1)
            clear { circle(dot, 3.2).fill() }
            circle(dot, 2.2).fill()
        default:
            break
        }
        ctx.compositingOperation = .sourceOver
    }

    private static func circle(_ c: NSPoint, _ r: CGFloat) -> NSBezierPath {
        NSBezierPath(ovalIn: NSRect(x: c.x - r, y: c.y - r, width: 2 * r, height: 2 * r))
    }

    /// Runs `body` with drawing that erases instead of paints.
    private static func clear(_ body: () -> Void) {
        guard let ctx = NSGraphicsContext.current else { return }
        ctx.saveGraphicsState()
        ctx.compositingOperation = .clear
        body()
        ctx.restoreGraphicsState()
    }
}
