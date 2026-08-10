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

    /// A rounded-square "icon chip": a soft-tinted background with a
    /// centered SF Symbol — the shared visual unit behind plugin cards, the
    /// Add Plugins tile, and the Hub's empty-state hero, so the
    /// NSView+layer-radius+fill boilerplate exists in exactly one place
    /// rather than copy-pasted at each call site. The returned view already
    /// carries its own fixed width/height constraints; callers only need to
    /// position it (leading/centerY/etc.) in their own layout.
    static func iconChip(diameter: CGFloat, radius: CGFloat? = nil, fill: NSColor,
                          symbol: String, symbolColor: NSColor, pointSize: CGFloat) -> NSView {
        let chip = NSView()
        chip.wantsLayer = true
        chip.layer?.backgroundColor = fill.cgColor
        chip.layer?.cornerRadius = radius ?? diameter / 4
        chip.translatesAutoresizingMaskIntoConstraints = false

        let icon = NSImageView()
        let cfg = NSImage.SymbolConfiguration(pointSize: pointSize, weight: .medium)
        icon.image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)?
            .withSymbolConfiguration(cfg)
        icon.contentTintColor = symbolColor
        icon.translatesAutoresizingMaskIntoConstraints = false
        chip.addSubview(icon)

        NSLayoutConstraint.activate([
            chip.widthAnchor.constraint(equalToConstant: diameter),
            chip.heightAnchor.constraint(equalToConstant: diameter),
            icon.centerXAnchor.constraint(equalTo: chip.centerXAnchor),
            icon.centerYAnchor.constraint(equalTo: chip.centerYAnchor)
        ])
        return chip
    }
}
