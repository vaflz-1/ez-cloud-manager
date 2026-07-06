import AppKit

/// Visual identity for plugin categories (DevOps/DevSecOps/AIOps) — mirrors
/// ProviderStyle's per-provider colors/badges, drawn in code rather than
/// shipped as assets for the same reasons (crisp at any scale, no trademark
/// concerns, new categories get a neutral treatment for free).
enum PluginStyle {
    static func color(_ category: String) -> NSColor {
        switch category {
        case "DevOps":    return .systemBlue
        case "DevSecOps": return .systemRed
        case "AIOps":     return .systemPurple
        default:          return .systemGray
        }
    }

    /// Small rounded "pill" badge with the category name — used on plugin
    /// cards (the Hub grid) and catalog rows (Add Plugins sheet).
    static func pill(_ category: String, height: CGFloat = 16) -> NSImage {
        let attrs: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: height * 0.6, weight: .semibold),
            .foregroundColor: NSColor.white
        ]
        let textSize = (category as NSString).size(withAttributes: attrs)
        let size = NSSize(width: ceil(textSize.width) + height * 0.9, height: height)
        return NSImage(size: size, flipped: false) { rect in
            NSBezierPath(roundedRect: rect, xRadius: height / 2, yRadius: height / 2).addClip()
            color(category).withAlphaComponent(0.85).setFill()
            rect.fill()
            (category as NSString).draw(
                at: NSPoint(x: (rect.width - textSize.width) / 2, y: (rect.height - textSize.height) / 2 - 0.5),
                withAttributes: attrs)
            return true
        }
    }
}
