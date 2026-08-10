import AppKit

/// Product-facing identity. Technical identifiers (`ezcloud`, bundle id and
/// on-disk EZCloudManager directories) intentionally remain stable during the
/// working-name phase so a visual rename can never strand user data.
enum Product {
    static let name = "Kervik"
    static let tagline = "Calm command for cloud work."

    static func workspaceTitle(_ workspace: String) -> String {
        "\(workspace) — \(name)"
    }

    static func toolTitle(_ tool: String, workspace: String) -> String {
        "\(tool) — \(workspace)"
    }
}

enum AppAppearance: Int, CaseIterable {
    case system
    case light
    case dark

    var title: String {
        switch self {
        case .system: return "System"
        case .light: return "Light"
        case .dark: return "Dark"
        }
    }

    var appearance: NSAppearance? {
        switch self {
        case .system: return nil
        case .light: return NSAppearance(named: .aqua)
        case .dark: return NSAppearance(named: .darkAqua)
        }
    }
}

/// Compact native design tokens. Color stays semantic so light mode, dark
/// mode and Increased Contrast remain platform-correct; the brand mark owns
/// the petrol/porcelain palette instead of tinting every control.
enum UI {
    static let space4: CGFloat = 4
    static let space8: CGFloat = 8
    static let space12: CGFloat = 12
    static let space16: CGFloat = 16
    static let space24: CGFloat = 24
    static let space32: CGFloat = 32

    static let pad = space24
    static let gap = space16
    static let cardPad = space16
    static let cardRadius: CGFloat = 8
    static let labelGap = space8
    static let rowHeight: CGFloat = 34

    static let pageTitleFont = NSFont.systemFont(ofSize: 24, weight: .semibold)
    static let sectionTitleFont = NSFont.systemFont(ofSize: 17, weight: .semibold)
    static let bodyFont = NSFont.systemFont(ofSize: 13)
    static let captionFont = NSFont.systemFont(ofSize: 11)
    static let monoFont = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)

    static func makeCard() -> NSBox {
        let box = NSBox()
        box.boxType = .custom
        box.titlePosition = .noTitle
        box.fillColor = .controlBackgroundColor
        box.borderColor = .separatorColor
        box.borderWidth = 1
        box.cornerRadius = cardRadius
        box.contentViewMargins = NSSize(width: cardPad, height: cardPad)
        box.translatesAutoresizingMaskIntoConstraints = false
        return box
    }

    static func sectionCaption(_ text: String) -> NSTextField {
        let label = NSTextField(labelWithString: text.uppercased())
        label.font = .systemFont(ofSize: 11, weight: .semibold)
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        return label
    }
}
