import AppKit

extension CloudAccountsWindowController {
    func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1180, height: 820),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        win.title = Product.toolTitle("Connections", workspace: profile.name)
        win.minSize = NSSize(width: 920, height: 660)
        win.titlebarAppearsTransparent = true
        win.titleVisibility = .visible
        win.toolbarStyle = .unified
        self.window = win

        // NSSplitViewController gives an automatic full-height, vibrant sidebar
        // aligned to the transparent titlebar — the modern macOS shell.
        let sidebarVC = NSViewController()
        sidebarVC.view = buildSidebar()
        let detailVC = NSViewController()
        detailVC.view = buildDetail()

        let splitVC = NSSplitViewController()
        let sidebarItem = NSSplitViewItem(sidebarWithViewController: sidebarVC)
        sidebarItem.minimumThickness = 220
        sidebarItem.maximumThickness = 320
        sidebarItem.canCollapse = true
        sidebarItem.holdingPriority = NSLayoutConstraint.Priority(260)
        splitVC.addSplitViewItem(sidebarItem)
        splitVC.addSplitViewItem(NSSplitViewItem(viewController: detailVC))
        // Per-profile: two Cloud Accounts windows must not fight over one
        // saved split width — and its own identifier, distinct from the
        // Hub's, so opening both for the same profile never collides.
        splitVC.splitView.autosaveName = "EZCloudManagerAccountsSplit-\(profile.id)"
        win.contentViewController = splitVC

        positionWindow(win)
    }

    /// Toolbar over the detail pane: sidebar toggle and refresh. Launch
    /// Templates and Ko-fi moved to the Hub (P1) — this window's toolbar is
    /// just the controls this editor itself needs.
    func configureToolbar() {
        let toolbar = NSToolbar(identifier: "EZCloudAccountsToolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly
        toolbar.allowsUserCustomization = false
        window?.toolbar = toolbar
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        switch itemIdentifier.rawValue {
        case "scope":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Visible Connections"
            item.toolTip = "Choose which connections this workspace shows"
            item.image = NSImage(systemSymbolName: "line.3.horizontal.decrease.circle", accessibilityDescription: "Visible Connections")
            item.target = self
            item.action = #selector(openScopeSheet)
            item.isBordered = true
            return item
        case "refresh":
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Refresh"
            item.image = NSImage(systemSymbolName: "arrow.clockwise", accessibilityDescription: "Refresh")
            item.target = self
            item.action = #selector(refreshTapped)
            item.isBordered = true
            return item
        default:
            return nil
        }
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.toggleSidebar, .flexibleSpace, .init("scope"), .init("refresh")]
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
        let autosaveName = NSWindow.FrameAutosaveName("EZCloudManagerAccountsWindow-\(profile.id)")
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

    func buildSidebar() -> NSView {
        let view = NSVisualEffectView()
        view.material = .sidebar
        view.blendingMode = .behindWindow
        view.state = .followsWindowActiveState
        view.translatesAutoresizingMaskIntoConstraints = false

        profileSearchField = NSSearchField()
        profileSearchField.placeholderString = "Search connections"
        profileSearchField.delegate = self
        profileSearchField.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(profileSearchField)

        profilesTable = NSTableView()
        profilesTable.headerView = nil
        profilesTable.delegate = self
        profilesTable.dataSource = self
        profilesTable.style = .sourceList
        profilesTable.allowsEmptySelection = true
        profilesTable.allowsMultipleSelection = false
        profilesTable.usesAlternatingRowBackgroundColors = false
        profilesTable.backgroundColor = .clear
        profilesTable.rowSizeStyle = .medium
        profilesTable.intercellSpacing = NSSize(width: 0, height: 2)
        profilesTable.floatsGroupRows = false
        let col = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("profile"))
        col.title = "Connection"
        col.width = 230
        profilesTable.addTableColumn(col)

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        scroll.documentView = profilesTable
        scroll.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(scroll)

        addRemoveControl = NSSegmentedControl(
            images: [
                NSImage(systemSymbolName: "plus", accessibilityDescription: "Add connection")!,
                NSImage(systemSymbolName: "minus", accessibilityDescription: "Delete connection")!
            ],
            trackingMode: .momentary,
            target: self,
            action: #selector(sidebarSegment(_:)))
        addRemoveControl.segmentStyle = .smallSquare
        addRemoveControl.controlSize = .small
        addRemoveControl.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(addRemoveControl)

        NSLayoutConstraint.activate([
            // Top inset clears the transparent titlebar / traffic lights.
            profileSearchField.topAnchor.constraint(equalTo: view.topAnchor, constant: 48),
            profileSearchField.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 10),
            profileSearchField.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -10),
            scroll.topAnchor.constraint(equalTo: profileSearchField.bottomAnchor, constant: 8),
            scroll.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 4),
            scroll.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -4),
            addRemoveControl.topAnchor.constraint(equalTo: scroll.bottomAnchor, constant: 8),
            addRemoveControl.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 10),
            addRemoveControl.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -10)
        ])

        return view
    }

    func roundedButton(title: String, systemImage: String? = nil, action: Selector) -> NSButton {
        let button = NSButton(title: title, target: self, action: action)
        button.bezelStyle = .rounded
        if let systemImage, let image = NSImage(systemSymbolName: systemImage, accessibilityDescription: title) {
            button.image = image
            button.imagePosition = title.count <= 1 ? .imageOnly : .imageLeading
        }
        return button
    }

    /// A rounded, hairline-bordered "card" — shared with Manage Profiles via
    /// UI.makeCard(); kept as a thin same-name wrapper here so this file's
    /// existing call sites (`makeCard()`, no `UI.` prefix) don't all need
    /// touching.
    func makeCard() -> NSBox { UI.makeCard() }

    /// Uppercase, secondary section caption — shared with Manage Profiles via
    /// UI.sectionCaption(_:); see makeCard()'s note above.
    func sectionCaption(_ text: String) -> NSTextField { UI.sectionCaption(text) }

    // buildDetail() — the Cards 1-3 detail-pane builder — lives in
    // CloudAccountsWindowController+DetailLayout.swift (split out to stay
    // under the file-size budget).
}
