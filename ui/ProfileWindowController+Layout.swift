import AppKit
import QuartzCore

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
        win.title = Product.workspaceTitle(profile.name)
        win.minSize = NSSize(width: 600, height: 460)
        win.titlebarAppearsTransparent = true
        win.titleVisibility = .visible
        win.toolbarStyle = .unified
        self.window = win
        win.contentView = buildHubContent()
        positionWindow(win)
    }

    /// The toolbar exposes only platform context: Workspace and Connections.
    /// Add-ons are discovered in content; support remains in Help.
    func configureToolbar() {
        let toolbar = NSToolbar(identifier: "EZCloudHubToolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconAndLabel
        toolbar.allowsUserCustomization = false
        window?.toolbar = toolbar
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        switch itemIdentifier.rawValue {
        case "profileSwitcher":
            return buildProfileSwitcherToolbarItem(identifier: itemIdentifier)
        case "connections":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Connections"
            item.paletteLabel = "Connections"
            item.toolTip = "Manage trusted cloud connections for this workspace"
            item.image = NSImage(systemSymbolName: "link", accessibilityDescription: "Connections")
            item.target = self
            item.action = #selector(openConnections)
            return item
        default:
            return nil
        }
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.init("profileSwitcher"), .flexibleSpace, .init("connections")]
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

    /// Builds both the empty state and the grid, stacked in the same place.
    /// Workspace identity remains in the toolbar; the content hierarchy tells
    /// the user what this surface is, which Connections policy it inherits,
    /// and what the next safe action is.
    private func buildHubContent() -> NSView {
        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false

        let title = NSTextField(labelWithString: "Add-ons")
        title.font = UI.pageTitleFont
        title.lineBreakMode = .byTruncatingTail
        title.translatesAutoresizingMaskIntoConstraints = false
        workspaceTitleLabel = title

        let meta = NSTextField(labelWithString: "")
        meta.font = UI.captionFont
        meta.textColor = .secondaryLabelColor
        meta.translatesAutoresizingMaskIntoConstraints = false
        workspaceMetaLabel = meta

        let browse = NSButton(title: "Browse Add-ons…", target: self, action: #selector(openCatalog))
        UI.style(browse, as: .secondary)
        browse.translatesAutoresizingMaskIntoConstraints = false
        browseAddonsButton = browse

        for view in [title, meta, browse] {
            container.addSubview(view)
        }

        let empty = buildEmptyState()
        container.addSubview(empty)
        emptyStateView = empty

        let gridHost = NSView()
        gridHost.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(gridHost)
        gridHostView = gridHost

        for view in [empty, gridHost] {
            NSLayoutConstraint.activate([
                view.topAnchor.constraint(equalTo: meta.bottomAnchor, constant: UI.space20),
                view.leadingAnchor.constraint(equalTo: container.leadingAnchor),
                view.trailingAnchor.constraint(equalTo: container.trailingAnchor),
                view.bottomAnchor.constraint(equalTo: container.bottomAnchor)
            ])
        }
        NSLayoutConstraint.activate([
            title.topAnchor.constraint(equalTo: container.safeAreaLayoutGuide.topAnchor, constant: UI.space24),
            title.leadingAnchor.constraint(equalTo: container.leadingAnchor, constant: UI.space24),
            title.trailingAnchor.constraint(lessThanOrEqualTo: browse.leadingAnchor, constant: -UI.space16),
            browse.trailingAnchor.constraint(equalTo: container.trailingAnchor, constant: -UI.space24),
            browse.centerYAnchor.constraint(equalTo: title.centerYAnchor),
            meta.topAnchor.constraint(equalTo: title.bottomAnchor, constant: UI.space4),
            meta.leadingAnchor.constraint(equalTo: title.leadingAnchor),
            meta.trailingAnchor.constraint(lessThanOrEqualTo: container.trailingAnchor, constant: -UI.space24)
        ])
        if !enabledPlugins.isEmpty { ensureGridBuilt() }
        updateWorkspaceHeader()
        return container
    }

    func updateWorkspaceHeader() {
        workspaceTitleLabel?.stringValue = "Add-ons"
        let settings = profile.cloudAccountsSettings
        let connectionPolicy: String
        if settings.showAllAccounts {
            connectionPolicy = "All discovered connections"
        } else if settings.accounts.isEmpty {
            connectionPolicy = "No connections allowed"
        } else {
            let noun = settings.accounts.count == 1 ? "connection" : "connections"
            connectionPolicy = "\(settings.accounts.count) allowed \(noun)"
        }
        let addOnNoun = enabledPlugins.count == 1 ? "add-on" : "add-ons"
        workspaceMetaLabel?.stringValue =
            "\(connectionPolicy)  ·  \(enabledPlugins.count) \(addOnNoun)  ·  Saved \(AppTimestamp.display(profile.savedAt))"
    }

    /// A compact working empty state. The brand is already present in the app
    /// icon and titlebar; this surface spends its space on orientation and
    /// the two useful next actions instead of repeating a decorative hero.
    private func buildEmptyState() -> NSView {
        let title = NSTextField(labelWithString: "No add-ons enabled")
        title.font = UI.sectionTitleFont
        title.alignment = .left

        let subtitle = NSTextField(wrappingLabelWithString:
            Product.emptyAddonsMessage)
        subtitle.font = UI.bodyFont
        subtitle.textColor = .secondaryLabelColor
        subtitle.alignment = .left
        subtitle.maximumNumberOfLines = 3

        let button = NSButton(title: "Browse Add-ons…", target: self, action: #selector(openCatalog))
        UI.style(button, as: .primary, large: true)
        emptyStateTitleLabel = title
        emptyStateSubtitleLabel = subtitle
        emptyStateButton = button

        let connections = NSButton(title: "Connections", target: self, action: #selector(openConnections))
        UI.style(connections, as: .secondary, large: true)

        let actions = NSStackView(views: [button, connections])
        actions.orientation = .horizontal
        actions.alignment = .centerY
        actions.spacing = UI.space8

        let stack = NSStackView(views: [title, subtitle, actions])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = UI.space8
        stack.setCustomSpacing(UI.space16, after: subtitle)
        stack.translatesAutoresizingMaskIntoConstraints = false

        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: container.topAnchor, constant: UI.space24),
            stack.leadingAnchor.constraint(equalTo: container.leadingAnchor, constant: UI.space24),
            stack.trailingAnchor.constraint(lessThanOrEqualTo: container.trailingAnchor, constant: -UI.space24),
            stack.widthAnchor.constraint(lessThanOrEqualToConstant: 520)
        ])
        return container
    }

    func configureEmptyStateForOnboarding() {
        emptyStateTitleLabel.stringValue = "No add-ons enabled"
        emptyStateSubtitleLabel.stringValue = Product.emptyAddonsMessage
        emptyStateButton.title = "Browse Add-ons…"
        emptyStateButton.target = self
        emptyStateButton.action = #selector(openCatalog)
    }

    func configureEmptyStateForError(_ message: String) {
        emptyStateTitleLabel.stringValue = "Add-ons unavailable"
        emptyStateSubtitleLabel.stringValue = message
        emptyStateButton.title = "Try Again"
        emptyStateButton.target = self
        emptyStateButton.action = #selector(retryHub)
    }

    /// The plugin grid: a flow layout of uniform cards, one per enabled plugin.
    func ensureGridBuilt() {
        guard gridScrollView == nil else { return }
        let grid = buildGrid()
        gridHostView.addSubview(grid)
        NSLayoutConstraint.activate([
            grid.topAnchor.constraint(equalTo: gridHostView.topAnchor),
            grid.leadingAnchor.constraint(equalTo: gridHostView.leadingAnchor),
            grid.trailingAnchor.constraint(equalTo: gridHostView.trailingAnchor),
            grid.bottomAnchor.constraint(equalTo: gridHostView.bottomAnchor)
        ])
        gridScrollView = grid
    }

    private func buildGrid() -> NSScrollView {
        let layout = NSCollectionViewFlowLayout()
        layout.itemSize = NSSize(width: 300, height: 112)
        layout.minimumInteritemSpacing = UI.gap
        layout.minimumLineSpacing = UI.gap
        layout.sectionInset = NSEdgeInsets(top: UI.space4, left: UI.pad, bottom: UI.pad, right: UI.pad)

        let collection = HubCollectionView()
        collection.onActivateSelection = { [weak self, weak collection] in
            guard let self, let indexPath = collection?.selectionIndexPaths.first else { return }
            self.activateHubItem(at: indexPath.item)
        }
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

/// A quiet native hover treatment for the Hub's interactive cards. The card
/// remains in the same plane: only its border changes, with no web-style lift,
/// shadow, scaling or pointing-hand cursor.
protocol HoverableCard: AnyObject {
    var hoverBox: NSBox? { get }
    var isCardSelected: Bool { get }
    var hoverProxy: HoverProxy? { get set }
}

extension HoverableCard {
    func installHoverTracking(on view: NSView) {
        let proxy = HoverProxy(handler: self)
        hoverProxy = proxy
        let area = NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
                                   owner: proxy, userInfo: nil)
        view.addTrackingArea(area)
    }

    func hoverEnter() {
        guard !isCardSelected else { return }
        guard let box = hoverBox else { return }
        box.wantsLayer = true
        box.borderColor = NSColor.controlAccentColor.withAlphaComponent(0.45)
        CATransaction.begin()
        CATransaction.setAnimationDuration(NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 0 : 0.10)
        CATransaction.setAnimationTimingFunction(CAMediaTimingFunction(name: .easeOut))
        CATransaction.commit()
    }

    func hoverExit() {
        guard !isCardSelected else { return }
        guard let box = hoverBox else { return }
        box.borderColor = .separatorColor
        CATransaction.begin()
        CATransaction.setAnimationDuration(NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 0 : 0.10)
        CATransaction.commit()
    }
}

/// Forwards NSTrackingArea mouse events to a HoverableCard without making
/// the card itself the tracking-area owner (NSCollectionViewItem is not an
/// NSResponder subclass suitable for that role in every AppKit version).
final class HoverProxy: NSResponder {
    weak var handler: HoverableCard?
    init(handler: HoverableCard) {
        self.handler = handler
        super.init()
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    override func mouseEntered(with event: NSEvent) { handler?.hoverEnter() }
    override func mouseExited(with event: NSEvent) { handler?.hoverExit() }
}

/// A real accessibility button surface for collection cards. Mouse selection
/// remains owned by NSCollectionView; VoiceOver AXPress calls the same action
/// directly instead of announcing a button that cannot be activated.
final class ActionCardBox: NSBox {
    var onPress: (() -> Void)?

    override func accessibilityPerformPress() -> Bool {
        guard let onPress else { return false }
        onPress()
        return true
    }
}

/// Adds explicit Return/Space activation to the selected Hub card.
final class HubCollectionView: NSCollectionView {
    var onActivateSelection: (() -> Void)?

    override func mouseUp(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        let clickedItem = indexPathForItem(at: point)
        super.mouseUp(with: event)
        if clickedItem != nil, event.clickCount == 1 {
            onActivateSelection?()
        }
    }

    override func keyDown(with event: NSEvent) {
        if event.keyCode == 36 || event.keyCode == 49 {
            onActivateSelection?()
            return
        }
        super.keyDown(with: event)
    }
}

/// One compact add-on card: neutral icon, workflow name and purpose, followed
/// by the connector contract. Category color is intentionally absent; trust
/// and capability matter here, not a marketplace taxonomy.
final class PluginCardItem: NSCollectionViewItem, HoverableCard {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("pluginCard")

    private let iconView = NSImageView()
    private let nameLabel = NSTextField(labelWithString: "")
    private let descriptionLabel = NSTextField(wrappingLabelWithString: "")
    private let connectorsLabel = NSTextField(labelWithString: "")
    var hoverBox: NSBox? { view as? NSBox }
    var isCardSelected: Bool { isSelected }
    var hoverProxy: HoverProxy?
    var onPress: (() -> Void)? {
        didSet { (view as? ActionCardBox)?.onPress = onPress }
    }

    override var isSelected: Bool {
        didSet { updateSelectionAppearance() }
    }

    override func loadView() {
        let card = ActionCardBox()
        card.boxType = .custom
        card.titlePosition = .noTitle
        card.fillColor = .controlBackgroundColor
        card.borderColor = .separatorColor
        card.borderWidth = 1
        card.cornerRadius = UI.cardRadius
        card.contentViewMargins = NSSize(width: UI.cardPad, height: UI.cardPad)
        view = card
        card.setAccessibilityElement(true)
        card.setAccessibilityRole(.button)
        card.onPress = onPress

        let content = card.contentView!
        iconView.contentTintColor = .secondaryLabelColor
        iconView.setAccessibilityElement(false)
        iconView.translatesAutoresizingMaskIntoConstraints = false
        nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.translatesAutoresizingMaskIntoConstraints = false
        descriptionLabel.font = .systemFont(ofSize: 11)
        descriptionLabel.textColor = .secondaryLabelColor
        descriptionLabel.maximumNumberOfLines = 2
        descriptionLabel.translatesAutoresizingMaskIntoConstraints = false
        connectorsLabel.font = UI.captionFont
        connectorsLabel.textColor = .tertiaryLabelColor
        connectorsLabel.lineBreakMode = .byTruncatingTail
        connectorsLabel.translatesAutoresizingMaskIntoConstraints = false

        content.addSubview(iconView)
        content.addSubview(nameLabel)
        content.addSubview(descriptionLabel)
        content.addSubview(connectorsLabel)

        NSLayoutConstraint.activate([
            iconView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            iconView.centerYAnchor.constraint(equalTo: nameLabel.centerYAnchor),
            iconView.widthAnchor.constraint(equalToConstant: 20),
            iconView.heightAnchor.constraint(equalToConstant: 20),
            nameLabel.topAnchor.constraint(equalTo: content.topAnchor),
            nameLabel.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: UI.space8),
            nameLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            descriptionLabel.topAnchor.constraint(equalTo: nameLabel.bottomAnchor, constant: UI.space8),
            descriptionLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            descriptionLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            descriptionLabel.bottomAnchor.constraint(lessThanOrEqualTo: connectorsLabel.topAnchor, constant: -UI.space4),
            connectorsLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            connectorsLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            connectorsLabel.bottomAnchor.constraint(equalTo: content.bottomAnchor)
        ])

        installHoverTracking(on: card)
        updateSelectionAppearance()
    }

    private func updateSelectionAppearance() {
        guard let card = view as? NSBox else { return }
        card.borderColor = isSelected ? .keyboardFocusIndicatorColor : .separatorColor
        card.borderWidth = isSelected ? 2 : 1
    }

    func configure(_ plugin: PluginDescriptor) {
        let configuration = NSImage.SymbolConfiguration(pointSize: 15, weight: .medium)
        iconView.image = NSImage(systemSymbolName: plugin.icon, accessibilityDescription: nil)?
            .withSymbolConfiguration(configuration)
        nameLabel.stringValue = plugin.name
        descriptionLabel.stringValue = plugin.description
        if plugin.clouds.isEmpty {
            connectorsLabel.stringValue = "No connection required"
        } else {
            let connectors = plugin.clouds.map { $0.uppercased() }.joined(separator: " · ")
            connectorsLabel.stringValue = "Requires \(connectors)"
        }
        view.setAccessibilityLabel(plugin.name)
        view.setAccessibilityHelp("\(plugin.description). \(connectorsLabel.stringValue).")
    }
}
