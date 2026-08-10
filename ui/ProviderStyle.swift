import AppKit

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

    /// Same drawn-dot approach as `dot(_:diameter:)`, but neutral — used
    /// wherever a single-color badge would misrepresent a global profile
    /// that spans multiple providers or shows every account (the Hub's
    /// profile switcher, in particular).
    static func neutralDot(diameter: CGFloat = 7) -> NSImage {
        NSImage(size: NSSize(width: diameter, height: diameter), flipped: false) { rect in
            NSColor.tertiaryLabelColor.setFill()
            NSBezierPath(ovalIn: rect).fill()
            return true
        }
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
