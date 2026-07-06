import AppKit

/// Shared spacing/shape system — generous, "inspector"-style breathing room.
enum UI {
    static let pad: CGFloat = 24        // content ↔ window edge
    static let gap: CGFloat = 20        // between cards
    static let cardPad: CGFloat = 16    // card interior padding
    static let cardRadius: CGFloat = 10
    static let labelGap: CGFloat = 8    // caption ↔ control
    static let rowHeight: CGFloat = 34
}

/// Window/toolbar/content construction for the Plugin Hub, split out to keep
/// the controller file focused on behavior (and under the size budget).
extension ProfileWindowController {
    func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 760, height: 560),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        win.title = "EZ Cloud Manager — \(profile.name)"
        win.minSize = NSSize(width: 600, height: 440)
        win.titlebarAppearsTransparent = true
        win.titleVisibility = .visible
        win.toolbarStyle = .unified
        self.window = win
        win.contentView = buildHubContent()
        positionWindow(win)
    }

    /// Toolbar: Add Plugins (opens the catalog sheet), Manage Profiles, Ko-fi
    /// — the heart is deliberately the quiet accent at the far right, visible
    /// on every launch, in nobody's way.
    func configureToolbar() {
        let toolbar = NSToolbar(identifier: "EZCloudHubToolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly
        toolbar.allowsUserCustomization = false
        window?.toolbar = toolbar
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        switch itemIdentifier.rawValue {
        case "addPlugins":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Add Plugins"
            item.toolTip = "Browse and enable plugins for this profile"
            item.image = NSImage(systemSymbolName: "plus.square.on.square", accessibilityDescription: "Add Plugins")
            item.target = self
            item.action = #selector(openCatalog)
            item.isBordered = true
            return item
        case "manageProfiles":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Manage Profiles"
            item.toolTip = "Create, rename, duplicate, or delete profiles"
            item.image = NSImage(systemSymbolName: "person.2.gearshape", accessibilityDescription: "Manage Profiles")
            item.target = self
            item.action = #selector(openManageProfiles)
            item.isBordered = true
            return item
        case "kofi":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Support"
            item.toolTip = "Enjoying EZ Cloud Manager? Support it on Ko-fi ♥"
            let cfg = NSImage.SymbolConfiguration(pointSize: 15, weight: .semibold)
            let heart = NSImage(systemSymbolName: "heart.fill", accessibilityDescription: "Support on Ko-fi")?
                .withSymbolConfiguration(cfg)
            let button = PulsingHeartButton(image: heart ?? NSImage(), target: self, action: #selector(openKoFi))
            button.isBordered = false
            button.contentTintColor = NSColor.systemPink
            item.view = button
            return item
        default:
            return nil
        }
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.init("addPlugins"), .init("manageProfiles"), .flexibleSpace, .init("kofi")]
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        toolbarDefaultItemIdentifiers(toolbar)
    }

    /// Restores this profile's last window position (persisted across
    /// launches, one autosave name per profile id) and makes sure it lands on
    /// a visible screen — a plain `center()` could place the window
    /// off-screen on multi-display setups, or a saved frame could point at a
    /// display that is no longer attached.
    private func positionWindow(_ win: NSWindow) {
        let autosaveName = NSWindow.FrameAutosaveName("EZCloudManagerWindow-\(profile.id)")
        win.setFrameAutosaveName(autosaveName)
        if !win.setFrameUsingName(autosaveName) {
            win.center()
        }
        guard !frameIsOnScreen(win.frame) else { return }

        win.center()
        if !frameIsOnScreen(win.frame), let visible = NSScreen.main?.visibleFrame {
            let size = win.frame.size
            win.setFrameOrigin(NSPoint(
                x: visible.midX - size.width / 2,
                y: visible.midY - size.height / 2
            ))
        }
    }

    /// True if `frame` overlaps some screen's visible area enough to be usable.
    private func frameIsOnScreen(_ frame: NSRect) -> Bool {
        for screen in NSScreen.screens {
            let overlap = screen.visibleFrame.intersection(frame)
            if overlap.width >= 200, overlap.height >= 150 {
                return true
            }
        }
        return false
    }

    /// Builds both the empty state and the grid, stacked in the same place —
    /// refreshHub() toggles which one is visible rather than swapping subviews.
    private func buildHubContent() -> NSView {
        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false

        let empty = buildEmptyState()
        container.addSubview(empty)
        emptyStateView = empty

        let grid = buildGrid()
        container.addSubview(grid)
        gridScrollView = grid

        for view in [empty, grid] {
            NSLayoutConstraint.activate([
                view.topAnchor.constraint(equalTo: container.safeAreaLayoutGuide.topAnchor),
                view.leadingAnchor.constraint(equalTo: container.leadingAnchor),
                view.trailingAnchor.constraint(equalTo: container.trailingAnchor),
                view.bottomAnchor.constraint(equalTo: container.bottomAnchor)
            ])
        }
        return container
    }

    /// The first-run (and always-empty-profile) moment: platform identity
    /// visible immediately, one prominent CTA into the catalog.
    private func buildEmptyState() -> NSView {
        let icon = NSImageView()
        let cfg = NSImage.SymbolConfiguration(pointSize: 56, weight: .regular)
        icon.image = NSImage(systemSymbolName: "square.grid.2x2.fill", accessibilityDescription: nil)?
            .withSymbolConfiguration(cfg)
        icon.contentTintColor = .tertiaryLabelColor

        let title = NSTextField(labelWithString: "No plugins installed")
        title.font = .systemFont(ofSize: 18, weight: .semibold)
        title.alignment = .center

        let subtitle = NSTextField(wrappingLabelWithString:
            "EZ Cloud Manager is a platform — every capability is a plugin. Install a few to get started.")
        subtitle.font = .systemFont(ofSize: 12)
        subtitle.textColor = .secondaryLabelColor
        subtitle.alignment = .center
        subtitle.maximumNumberOfLines = 2

        let button = NSButton(title: "Add Plugins…", target: self, action: #selector(openCatalog))
        button.bezelStyle = .rounded
        button.controlSize = .large

        let stack = NSStackView(views: [icon, title, subtitle, button])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 10
        stack.setCustomSpacing(18, after: icon)
        stack.setCustomSpacing(16, after: subtitle)
        stack.translatesAutoresizingMaskIntoConstraints = false

        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: container.centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: container.centerYAnchor),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 360)
        ])
        return container
    }

    /// The plugin grid: a flow layout of uniform cards, one per enabled plugin.
    private func buildGrid() -> NSScrollView {
        let layout = NSCollectionViewFlowLayout()
        layout.itemSize = NSSize(width: 200, height: 120)
        layout.minimumInteritemSpacing = UI.gap
        layout.minimumLineSpacing = UI.gap
        layout.sectionInset = NSEdgeInsets(top: UI.pad, left: UI.pad, bottom: UI.pad, right: UI.pad)

        let collection = NSCollectionView()
        collection.collectionViewLayout = layout
        collection.dataSource = self
        collection.delegate = self
        collection.isSelectable = true
        collection.backgroundColors = [.clear]
        collection.register(PluginCardItem.self, forItemWithIdentifier: PluginCardItem.reuseIdentifier)
        collectionView = collection

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.documentView = collection
        scroll.translatesAutoresizingMaskIntoConstraints = false
        return scroll
    }
}

/// One plugin card in the Hub grid: icon (tinted by category), name,
/// description, category pill.
final class PluginCardItem: NSCollectionViewItem {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("pluginCard")

    private let iconView = NSImageView()
    private let nameLabel = NSTextField(labelWithString: "")
    private let descriptionLabel = NSTextField(wrappingLabelWithString: "")
    private let pillView = NSImageView()

    override func loadView() {
        let card = NSBox()
        card.boxType = .custom
        card.titlePosition = .noTitle
        card.fillColor = .controlBackgroundColor
        card.borderColor = .separatorColor
        card.borderWidth = 1
        card.cornerRadius = UI.cardRadius
        card.contentViewMargins = NSSize(width: UI.cardPad, height: UI.cardPad)
        view = card

        let content = card.contentView!
        iconView.translatesAutoresizingMaskIntoConstraints = false
        nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.translatesAutoresizingMaskIntoConstraints = false
        descriptionLabel.font = .systemFont(ofSize: 11)
        descriptionLabel.textColor = .secondaryLabelColor
        descriptionLabel.maximumNumberOfLines = 2
        descriptionLabel.translatesAutoresizingMaskIntoConstraints = false
        pillView.translatesAutoresizingMaskIntoConstraints = false

        content.addSubview(iconView)
        content.addSubview(nameLabel)
        content.addSubview(descriptionLabel)
        content.addSubview(pillView)

        NSLayoutConstraint.activate([
            iconView.topAnchor.constraint(equalTo: content.topAnchor),
            iconView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            iconView.widthAnchor.constraint(equalToConstant: 24),
            iconView.heightAnchor.constraint(equalToConstant: 24),
            nameLabel.centerYAnchor.constraint(equalTo: iconView.centerYAnchor),
            nameLabel.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: 8),
            nameLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            descriptionLabel.topAnchor.constraint(equalTo: iconView.bottomAnchor, constant: 8),
            descriptionLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            descriptionLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            pillView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            pillView.bottomAnchor.constraint(equalTo: content.bottomAnchor)
        ])
    }

    func configure(_ plugin: PluginDescriptor) {
        let cfg = NSImage.SymbolConfiguration(pointSize: 18, weight: .medium)
        iconView.image = NSImage(systemSymbolName: plugin.icon, accessibilityDescription: plugin.name)?
            .withSymbolConfiguration(cfg)
        iconView.contentTintColor = PluginStyle.color(plugin.category)
        nameLabel.stringValue = plugin.name
        descriptionLabel.stringValue = plugin.description
        pillView.image = PluginStyle.pill(plugin.category)
    }
}
