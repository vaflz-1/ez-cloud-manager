import AppKit
import QuartzCore

/// Per-provider visual identity: accent colors, compact badges and row dots.
///
/// Badges are drawn in code, not shipped as assets: they stay crisp at any
/// scale, follow the brand hue of each cloud without using trademarked logo
/// artwork (App Store safe), and new providers get a neutral badge for free.
enum ProviderStyle {
    static func color(_ id: String) -> NSColor {
        switch id {
        case "aws":
            return NSColor(calibratedRed: 1.00, green: 0.60, blue: 0.00, alpha: 1) // AWS #FF9900
        case "gcp":
            return NSColor(calibratedRed: 0.26, green: 0.52, blue: 0.96, alpha: 1) // Google blue
        case "azure":
            return NSColor(calibratedRed: 0.00, green: 0.47, blue: 0.83, alpha: 1) // Azure blue
        default:
            return .systemGray
        }
    }

    static func shortLabel(_ id: String) -> String {
        switch id {
        case "aws":   return "AWS"
        case "gcp":   return "GCP"
        case "azure": return "AZ"
        default:      return String(id.prefix(3)).uppercased()
        }
    }

    /// Rounded-rect badge with the provider's initials (sidebar headers,
    /// provider popup). White bold text on the brand color reads correctly
    /// in both light and dark appearance.
    static func badge(_ id: String, height: CGFloat = 15) -> NSImage {
        let label = shortLabel(id)
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: height * 0.56, weight: .bold),
            .foregroundColor: NSColor.white
        ]
        let textSize = (label as NSString).size(withAttributes: attrs)
        let size = NSSize(width: max(height, ceil(textSize.width) + height * 0.55), height: height)
        return NSImage(size: size, flipped: false) { rect in
            NSBezierPath(roundedRect: rect, xRadius: height * 0.3, yRadius: height * 0.3).addClip()
            color(id).setFill()
            rect.fill()
            (label as NSString).draw(
                at: NSPoint(x: (rect.width - textSize.width) / 2, y: (rect.height - textSize.height) / 2 - 0.5),
                withAttributes: attrs)
            return true
        }
    }

    /// Small tinted dot marking a profile row's provider — keeps the color
    /// code visible when search flattens the grouped list.
    static func dot(_ id: String, diameter: CGFloat = 7) -> NSImage {
        NSImage(size: NSSize(width: diameter, height: diameter), flipped: false) { rect in
            color(id).withAlphaComponent(0.9).setFill()
            NSBezierPath(ovalIn: rect).fill()
            return true
        }
    }
}

/// The Ko-fi heart: quiet at rest, beats while hovered, pops on click.
/// A donation button must never nag — motion only happens when the user is
/// already pointing at it.
final class PulsingHeartButton: NSButton {
    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        wantsLayer = true
        alphaValue = 0.85 // rest state sits back; hover lifts to full pink
        recenterAnchor()
    }

    override func layout() {
        super.layout()
        recenterAnchor()
    }

    /// Scale animations must pivot around the heart's center, not the
    /// default bottom-left anchor.
    private func recenterAnchor() {
        guard let layer else { return }
        let frame = layer.frame
        layer.anchorPoint = CGPoint(x: 0.5, y: 0.5)
        layer.frame = frame
    }

    override func updateTrackingAreas() {
        trackingAreas.forEach(removeTrackingArea)
        addTrackingArea(NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self))
        super.updateTrackingAreas()
    }

    override func mouseEntered(with event: NSEvent) {
        super.mouseEntered(with: event)
        animator().alphaValue = 1.0
        guard layer?.animation(forKey: "heartbeat") == nil else { return }
        let beat = CAKeyframeAnimation(keyPath: "transform.scale")
        beat.values = [1.0, 1.28, 0.96, 1.18, 1.0]        // lub-dub
        beat.keyTimes = [0, 0.22, 0.45, 0.68, 1]
        beat.duration = 0.9
        beat.repeatCount = .infinity
        beat.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
        layer?.add(beat, forKey: "heartbeat")
        toolTip = "Support EZ Cloud Manager on Ko-fi ♥"
    }

    override func mouseExited(with event: NSEvent) {
        super.mouseExited(with: event)
        animator().alphaValue = 0.85
        layer?.removeAnimation(forKey: "heartbeat")
    }

    override func mouseDown(with event: NSEvent) {
        // A single grateful pop, then the normal action fires.
        let pop = CABasicAnimation(keyPath: "transform.scale")
        pop.fromValue = 1.0
        pop.toValue = 1.45
        pop.duration = 0.12
        pop.autoreverses = true
        layer?.add(pop, forKey: "pop")
        super.mouseDown(with: event)
    }
}

/// Sidebar provider group header: [badge] DISPLAY NAME …………… count.
final class ProviderHeaderView: NSTableCellView {
    private let badgeView = NSImageView()
    private let titleLabel = NSTextField(labelWithString: "")
    private let countLabel = NSTextField(labelWithString: "")

    init(reuseIdentifier: NSUserInterfaceItemIdentifier) {
        super.init(frame: .zero)
        identifier = reuseIdentifier

        badgeView.translatesAutoresizingMaskIntoConstraints = false
        addSubview(badgeView)

        titleLabel.font = .systemFont(ofSize: 11, weight: .semibold)
        titleLabel.textColor = .secondaryLabelColor
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(titleLabel)

        countLabel.font = .monospacedDigitSystemFont(ofSize: 11, weight: .medium)
        countLabel.textColor = .secondaryLabelColor
        countLabel.translatesAutoresizingMaskIntoConstraints = false
        addSubview(countLabel)

        NSLayoutConstraint.activate([
            badgeView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
            badgeView.centerYAnchor.constraint(equalTo: centerYAnchor, constant: 2),
            titleLabel.leadingAnchor.constraint(equalTo: badgeView.trailingAnchor, constant: 6),
            titleLabel.centerYAnchor.constraint(equalTo: badgeView.centerYAnchor),
            countLabel.leadingAnchor.constraint(equalTo: titleLabel.trailingAnchor, constant: 5),
            countLabel.centerYAnchor.constraint(equalTo: badgeView.centerYAnchor),
            countLabel.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -6)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(providerID: String, title: String, count: Int) {
        badgeView.image = ProviderStyle.badge(providerID)
        // Count rides next to the name: the trailing edge of a source-list
        // group row is unreliable real estate (overlay scroller + the row's
        // own hover affordances), so a right-aligned label can vanish there.
        titleLabel.stringValue = title.uppercased()
        countLabel.stringValue = count > 0 ? "· \(count)" : ""
    }
}
