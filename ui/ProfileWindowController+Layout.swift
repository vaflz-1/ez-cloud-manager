import AppKit
import QuartzCore

/// Shared spacing/shape system — generous, "inspector"-style breathing room.
enum UI {
    static let pad: CGFloat = 24        // content ↔ window edge
    static let gap: CGFloat = 20        // between cards
    static let cardPad: CGFloat = 16    // card interior padding
    static let cardRadius: CGFloat = 10
    static let labelGap: CGFloat = 8    // caption ↔ control
    static let rowHeight: CGFloat = 34

    /// A rounded, hairline-bordered "card" (NSBox tracks appearance
    /// automatically). Add content to `.contentView`; interior padding comes
    /// from contentViewMargins. Shared across every window that adopts the
    /// card layout (Cloud Accounts, Manage Profiles) so the boilerplate
    /// exists once.
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

    /// Uppercase, secondary section caption (System Settings / inspector style).
    static func sectionCaption(_ text: String) -> NSTextField {
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: 11, weight: .semibold)
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        return label
    }
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

    /// Toolbar: the profile switcher (leading — see +ProfileBar.swift),
    /// Add Plugins (opens the catalog sheet), Ko-fi — the heart is
    /// deliberately the quiet accent at the far right, visible on every
    /// launch, in nobody's way. "Manage Profiles" is no longer its own item —
    /// it's folded into the profile switcher's menu.
    func configureToolbar() {
        let toolbar = NSToolbar(identifier: "EZCloudHubToolbar")
        toolbar.delegate = self
        // .iconAndLabel (not .iconOnly) so "Add Plugins" reads as text, not
        // just a glyph — a small, deliberate ask from the design review.
        toolbar.displayMode = .iconAndLabel
        toolbar.allowsUserCustomization = false
        window?.toolbar = toolbar
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        switch itemIdentifier.rawValue {
        case "profileSwitcher":
            return buildProfileSwitcherToolbarItem(identifier: itemIdentifier)
        case "addPlugins":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Add Plugins"
            item.toolTip = "Browse and enable plugins for this profile"
            item.image = NSImage(systemSymbolName: "plus.square.on.square", accessibilityDescription: "Add Plugins")
            item.target = self
            item.action = #selector(openCatalog)
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
        [.init("profileSwitcher"), .init("addPlugins"), .flexibleSpace, .init("kofi")]
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
    /// refreshHub() toggles which one is visible rather than swapping
    /// subviews. The profile switcher lives in the toolbar (+ProfileBar.swift),
    /// not a separate view here — no vertical space to reserve for it.
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
        let iconChip = PluginStyle.iconChip(
            diameter: 72, radius: 16, fill: NSColor.controlAccentColor.withAlphaComponent(0.12),
            symbol: "square.grid.2x2.fill", symbolColor: .controlAccentColor, pointSize: 34)

        let title = NSTextField(labelWithString: "Build your cloud workspace")
        title.font = .systemFont(ofSize: 18, weight: .semibold)
        title.alignment = .center

        let subtitle = NSTextField(wrappingLabelWithString:
            "EZ Cloud Manager is a platform — every capability (accounts, launch templates, and more) is a plugin. Install a few to get started.")
        subtitle.font = .systemFont(ofSize: 12)
        subtitle.textColor = .secondaryLabelColor
        subtitle.alignment = .center
        subtitle.maximumNumberOfLines = 3

        let button = NSButton(title: "Add Plugins…", target: self, action: #selector(openCatalog))
        button.bezelStyle = .rounded
        button.controlSize = .large

        let hint = NSTextField(labelWithString: "Switch or create profiles from the ▾ menu at the top left.")
        hint.font = .systemFont(ofSize: 11)
        hint.textColor = .tertiaryLabelColor
        hint.alignment = .center

        let stack = NSStackView(views: [iconChip, title, subtitle, button, hint])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 10
        stack.setCustomSpacing(18, after: iconChip)
        stack.setCustomSpacing(16, after: subtitle)
        stack.setCustomSpacing(14, after: button)
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
        layout.itemSize = NSSize(width: 220, height: 132)
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
        collection.register(AddPluginTileItem.self, forItemWithIdentifier: AddPluginTileItem.reuseIdentifier)
        collectionView = collection

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.documentView = collection
        scroll.translatesAutoresizingMaskIntoConstraints = false
        return scroll
    }
}

/// A shared hover treatment for the Hub's interactive cards (plugin cards +
/// the Add Plugins tile) — NOT applied to static/editor cards elsewhere.
/// Border tints toward the accent color and a soft shadow lifts in, cursor
/// becomes a pointing hand; both revert on exit. Kept as a mixin protocol
/// rather than a shared base class since NSCollectionViewItem subclasses
/// can't share an implementation base beyond it.
protocol HoverableCard: AnyObject {
    var hoverBox: NSBox? { get }
    var hoverTintColor: NSColor { get }
}

extension HoverableCard {
    func installHoverTracking(on view: NSView) {
        let area = NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
                                   owner: HoverProxy(handler: self), userInfo: nil)
        view.addTrackingArea(area)
    }

    func hoverEnter() {
        guard let box = hoverBox else { return }
        box.wantsLayer = true
        box.borderColor = hoverTintColor.withAlphaComponent(0.5)
        CATransaction.begin()
        CATransaction.setAnimationDuration(0.12)
        CATransaction.setAnimationTimingFunction(CAMediaTimingFunction(name: .easeOut))
        box.layer?.shadowColor = NSColor.black.cgColor
        box.layer?.shadowOpacity = 0.12
        box.layer?.shadowRadius = 8
        box.layer?.shadowOffset = NSSize(width: 0, height: -2)
        CATransaction.commit()
        NSCursor.pointingHand.set()
    }

    func hoverExit() {
        guard let box = hoverBox else { return }
        box.borderColor = .separatorColor
        CATransaction.begin()
        CATransaction.setAnimationDuration(0.12)
        box.layer?.shadowOpacity = 0
        CATransaction.commit()
        NSCursor.arrow.set()
    }
}

/// Forwards NSTrackingArea mouse events to a HoverableCard without making
/// the card itself the tracking-area owner (NSCollectionViewItem is not an
/// NSResponder subclass suitable for that role in every AppKit version).
private final class HoverProxy: NSResponder {
    weak var handler: HoverableCard?
    init(handler: HoverableCard) {
        self.handler = handler
        super.init()
    }
    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }
    override func mouseEntered(with event: NSEvent) { handler?.hoverEnter() }
    override func mouseExited(with event: NSEvent) { handler?.hoverExit() }
}

/// One plugin card in the Hub grid (220×132): a 32×32 icon chip + name in
/// the header, a 2-line description body, and a footer row of the category
/// pill (leading) plus provider badges (trailing, one per `plugin.clouds`
/// entry — empty for a cloud-agnostic plugin like Transfer).
final class PluginCardItem: NSCollectionViewItem, HoverableCard {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("pluginCard")

    private let nameLabel = NSTextField(labelWithString: "")
    private let descriptionLabel = NSTextField(wrappingLabelWithString: "")
    private let pillView = NSImageView()
    private let badgesStack = NSStackView()
    private var chip: NSView?
    var hoverBox: NSBox? { view as? NSBox }
    var hoverTintColor: NSColor = .controlAccentColor

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
        nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.translatesAutoresizingMaskIntoConstraints = false
        descriptionLabel.font = .systemFont(ofSize: 11)
        descriptionLabel.textColor = .secondaryLabelColor
        descriptionLabel.maximumNumberOfLines = 2
        descriptionLabel.translatesAutoresizingMaskIntoConstraints = false
        pillView.translatesAutoresizingMaskIntoConstraints = false
        badgesStack.orientation = .horizontal
        badgesStack.spacing = 3
        badgesStack.translatesAutoresizingMaskIntoConstraints = false

        content.addSubview(nameLabel)
        content.addSubview(descriptionLabel)
        content.addSubview(pillView)
        content.addSubview(badgesStack)

        installHoverTracking(on: card)
    }

    func configure(_ plugin: PluginDescriptor) {
        chip?.removeFromSuperview()
        let categoryColor = PluginStyle.color(plugin.category)
        hoverTintColor = categoryColor
        let newChip = PluginStyle.iconChip(diameter: 32, radius: 8, fill: categoryColor.withAlphaComponent(0.15),
                                            symbol: plugin.icon, symbolColor: categoryColor, pointSize: 15)
        guard let content = view as? NSBox, let contentView = content.contentView else { return }
        contentView.addSubview(newChip)
        chip = newChip

        NSLayoutConstraint.activate([
            newChip.topAnchor.constraint(equalTo: contentView.topAnchor),
            newChip.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            nameLabel.centerYAnchor.constraint(equalTo: newChip.centerYAnchor),
            nameLabel.leadingAnchor.constraint(equalTo: newChip.trailingAnchor, constant: 8),
            nameLabel.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
            descriptionLabel.topAnchor.constraint(equalTo: newChip.bottomAnchor, constant: 8),
            descriptionLabel.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            descriptionLabel.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
            pillView.leadingAnchor.constraint(equalTo: contentView.leadingAnchor),
            pillView.bottomAnchor.constraint(equalTo: contentView.bottomAnchor),
            badgesStack.trailingAnchor.constraint(equalTo: contentView.trailingAnchor),
            badgesStack.centerYAnchor.constraint(equalTo: pillView.centerYAnchor),
            badgesStack.leadingAnchor.constraint(greaterThanOrEqualTo: pillView.trailingAnchor, constant: 6)
        ])

        nameLabel.stringValue = plugin.name
        descriptionLabel.stringValue = plugin.description
        pillView.image = PluginStyle.pill(plugin.category)

        badgesStack.arrangedSubviews.forEach { $0.removeFromSuperview() }
        for cloud in plugin.clouds {
            let badge = NSImageView()
            badge.image = ProviderStyle.badge(cloud, height: 13)
            badgesStack.addArrangedSubview(badge)
        }
    }
}

/// The "Add Plugins" tile (same 220×132 footprint) — a permanent,
/// dashed-border card always last in the Hub grid (see
/// numberOfItemsInSection/itemForRepresentedObjectAt in +Hub.swift), so
/// growing the plugin set is discoverable from inside the grid itself, not
/// only the toolbar button or the empty-state CTA.
final class AddPluginTileItem: NSCollectionViewItem, HoverableCard {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("addPluginTile")
    var hoverBox: NSBox? { view as? NSBox }
    let hoverTintColor: NSColor = .controlAccentColor

    override func loadView() {
        let card = NSBox()
        card.boxType = .custom
        card.titlePosition = .noTitle
        card.fillColor = .clear
        card.borderColor = .separatorColor
        card.borderWidth = 1.5
        card.cornerRadius = UI.cardRadius
        card.contentViewMargins = NSSize(width: UI.cardPad, height: UI.cardPad)
        view = card
        // Dashed border: NSBox draws a solid border, so overlay a dashed
        // CAShapeLayer matching its bounds/radius.
        card.wantsLayer = true

        let iconChip = PluginStyle.iconChip(
            diameter: 44, radius: 12, fill: NSColor.controlAccentColor.withAlphaComponent(0.10),
            symbol: "plus", symbolColor: .controlAccentColor, pointSize: 18)

        let title = NSTextField(labelWithString: "Add Plugins")
        title.font = .systemFont(ofSize: 13, weight: .semibold)

        let subtitle = NSTextField(labelWithString: "Browse the catalog")
        subtitle.font = .systemFont(ofSize: 11)
        subtitle.textColor = .tertiaryLabelColor

        let stack = NSStackView(views: [iconChip, title, subtitle])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 6
        stack.translatesAutoresizingMaskIntoConstraints = false

        let content = card.contentView!
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: content.centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: content.centerYAnchor)
        ])

        installHoverTracking(on: card)
    }

    override func viewDidLayout() {
        super.viewDidLayout()
        guard let card = view as? NSBox else { return }
        card.borderWidth = 0 // NSBox's own solid border is replaced by the dashed overlay below
        let dashed = (card.layer?.sublayers?.first { $0.name == "dashedBorder" } as? CAShapeLayer) ?? {
            let layer = CAShapeLayer()
            layer.name = "dashedBorder"
            layer.fillColor = NSColor.clear.cgColor
            layer.lineDashPattern = [5, 3]
            layer.lineWidth = 1.5
            card.layer?.addSublayer(layer)
            return layer
        }()
        dashed.strokeColor = NSColor.separatorColor.cgColor
        dashed.frame = card.bounds
        dashed.path = CGPath(roundedRect: card.bounds.insetBy(dx: 0.75, dy: 0.75),
                              cornerWidth: UI.cardRadius, cornerHeight: UI.cardRadius, transform: nil)
    }
}
