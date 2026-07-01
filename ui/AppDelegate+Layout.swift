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

extension AppDelegate {
    func buildWindow() {
        window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1180, height: 820),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        window.title = "EZ Cloud Manager"
        window.minSize = NSSize(width: 920, height: 640)
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .visible
        window.toolbarStyle = .unified

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
        splitVC.splitView.autosaveName = "EZCloudManagerSplit"
        window.contentViewController = splitVC

        positionWindow()
    }

    /// Toolbar over the detail pane: sidebar toggle + refresh (search stays in the sidebar).
    func configureToolbar() {
        let toolbar = NSToolbar(identifier: "EZCloudMainToolbar")
        toolbar.delegate = self
        toolbar.displayMode = .iconOnly
        toolbar.allowsUserCustomization = false
        window.toolbar = toolbar
    }

    func toolbar(_ toolbar: NSToolbar, itemForItemIdentifier itemIdentifier: NSToolbarItem.Identifier, willBeInsertedIntoToolbar flag: Bool) -> NSToolbarItem? {
        if itemIdentifier == .init("refresh") {
            let item = NSToolbarItem(itemIdentifier: itemIdentifier)
            item.label = "Refresh"
            item.image = NSImage(systemSymbolName: "arrow.clockwise", accessibilityDescription: "Refresh")
            item.target = self
            item.action = #selector(refreshTapped)
            item.isBordered = true
            return item
        }
        return nil
    }

    func toolbarDefaultItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        [.toggleSidebar, .flexibleSpace, .init("refresh")]
    }

    func toolbarAllowedItemIdentifiers(_ toolbar: NSToolbar) -> [NSToolbarItem.Identifier] {
        toolbarDefaultItemIdentifiers(toolbar)
    }

    /// Restores the last window position (persisted across launches) and makes
    /// sure it lands on a visible screen — a plain `center()` could place the
    /// window off-screen on multi-display setups, or a saved frame could point
    /// at a display that is no longer attached.
    private func positionWindow() {
        let autosaveName = NSWindow.FrameAutosaveName("EZCloudManagerMainWindow")
        window.setFrameAutosaveName(autosaveName)
        if !window.setFrameUsingName(autosaveName) {
            window.center()
        }
        guard !frameIsOnScreen(window.frame) else { return }

        window.center()
        if !frameIsOnScreen(window.frame), let visible = NSScreen.main?.visibleFrame {
            let size = window.frame.size
            window.setFrameOrigin(NSPoint(
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
        profileSearchField.placeholderString = "Search profiles"
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
        let col = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("profile"))
        col.title = "Profile"
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
                NSImage(systemSymbolName: "plus", accessibilityDescription: "Add profile")!,
                NSImage(systemSymbolName: "minus", accessibilityDescription: "Delete profile")!
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
            profileSearchField.topAnchor.constraint(equalTo: view.topAnchor, constant: 52),
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

    /// A rounded, hairline-bordered "card" (NSBox tracks appearance automatically).
    /// Add content to `.contentView`; interior padding comes from contentViewMargins.
    func makeCard() -> NSBox {
        let box = NSBox()
        box.boxType = .custom
        box.titlePosition = .noTitle
        box.fillColor = .controlBackgroundColor
        box.borderColor = .separatorColor
        box.borderWidth = 1
        box.cornerRadius = UI.cardRadius
        box.contentViewMargins = NSSize(width: UI.cardPad, height: UI.cardPad)
        box.translatesAutoresizingMaskIntoConstraints = false
        return box
    }

    /// Uppercase, secondary section caption (System Settings / inspector style).
    func sectionCaption(_ text: String) -> NSTextField {
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: 11, weight: .semibold)
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        return label
    }

    func buildDetail() -> NSView {
        let view = NSView()
        view.translatesAutoresizingMaskIntoConstraints = false

        // ── Card 1 · Profile name ───────────────────────────────────────────
        let profileCard = makeCard()
        view.addSubview(profileCard)
        let c1 = profileCard.contentView!

        let nameLabel = sectionCaption("PROFILE NAME")
        c1.addSubview(nameLabel)

        profileModeLabel = NSTextField(labelWithString: "New profile")
        profileModeLabel.font = .systemFont(ofSize: 11, weight: .medium)
        profileModeLabel.textColor = .secondaryLabelColor
        profileModeLabel.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(profileModeLabel)

        profileNameField = NSTextField()
        profileNameField.placeholderString = "example-profile"
        profileNameField.controlSize = .large
        profileNameField.delegate = self
        profileNameField.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(profileNameField)

        // ── Card 2 · Paste block ────────────────────────────────────────────
        let pasteCard = makeCard()
        view.addSubview(pasteCard)
        let c2 = pasteCard.contentView!

        let pasteLabel = sectionCaption("PASTE AWS CREDENTIALS OR CONFIG")
        c2.addSubview(pasteLabel)

        let pasteScroll = NSScrollView()
        pasteScroll.hasVerticalScroller = true
        pasteScroll.borderType = .noBorder
        pasteScroll.drawsBackground = false
        pasteScroll.translatesAutoresizingMaskIntoConstraints = false
        pasteView = NSTextView()
        pasteView.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        pasteView.textContainerInset = NSSize(width: 4, height: 6)
        pasteView.drawsBackground = false
        pasteView.delegate = self
        pasteScroll.documentView = pasteView
        c2.addSubview(pasteScroll)

        // ── Card 3 · Variables ──────────────────────────────────────────────
        let varsCard = makeCard()
        view.addSubview(varsCard)
        let c3 = varsCard.contentView!

        variablesTitleLabel = sectionCaption("VARIABLES")
        c3.addSubview(variablesTitleLabel)

        variablesSummaryLabel = NSTextField(labelWithString: "No profile variables loaded")
        variablesSummaryLabel.font = .systemFont(ofSize: 11, weight: .regular)
        variablesSummaryLabel.textColor = .tertiaryLabelColor
        variablesSummaryLabel.translatesAutoresizingMaskIntoConstraints = false
        c3.addSubview(variablesSummaryLabel)

        fieldsTable = NSTableView()
        fieldsTable.delegate = self
        fieldsTable.dataSource = self
        fieldsTable.usesAlternatingRowBackgroundColors = false
        fieldsTable.gridStyleMask = []
        fieldsTable.style = .inset
        fieldsTable.rowHeight = UI.rowHeight
        fieldsTable.intercellSpacing = NSSize(width: 0, height: 0)
        fieldsTable.selectionHighlightStyle = .regular
        fieldsTable.floatsGroupRows = false
        fieldsTable.columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle
        fieldsTable.allowsColumnResizing = false
        fieldsTable.allowsColumnSelection = false
        fieldsTable.backgroundColor = .clear

        let keyCol = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("key"))
        keyCol.title = "Variable"
        keyCol.width = 230
        let valueCol = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("value"))
        valueCol.title = "Value"
        valueCol.width = 520
        fieldsTable.addTableColumn(keyCol)
        fieldsTable.addTableColumn(valueCol)

        let fieldsScroll = NSScrollView()
        fieldsScroll.hasVerticalScroller = true
        fieldsScroll.borderType = .noBorder
        fieldsScroll.drawsBackground = false
        fieldsScroll.documentView = fieldsTable
        fieldsScroll.translatesAutoresizingMaskIntoConstraints = false
        c3.addSubview(fieldsScroll)

        let addVariableButton = roundedButton(title: "Add", systemImage: "plus", action: #selector(addVariable))
        let removeVariableButton = roundedButton(title: "Remove", systemImage: "minus", action: #selector(removeVariable))
        let copyValueButton = roundedButton(title: "Copy value", systemImage: "doc.on.doc", action: #selector(copyFieldValue))
        let editorButtons = NSStackView(views: [addVariableButton, removeVariableButton, copyValueButton])
        editorButtons.orientation = .horizontal
        editorButtons.spacing = 8
        editorButtons.translatesAutoresizingMaskIntoConstraints = false
        c3.addSubview(editorButtons)

        // ── Footer · status + primary action ────────────────────────────────
        statusLabel = NSTextField(labelWithString: "Ready")
        statusLabel.font = .systemFont(ofSize: 11, weight: .regular)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(statusLabel)

        saveButton = NSButton(title: "Save Profile", target: self, action: #selector(saveProfile))
        saveButton.bezelStyle = .push
        saveButton.controlSize = .large
        saveButton.keyEquivalent = "\r"
        saveButton.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(saveButton)

        NSLayoutConstraint.activate([
            // Card 1 — Profile
            profileCard.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: UI.pad),
            profileCard.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            profileCard.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -UI.pad),
            nameLabel.topAnchor.constraint(equalTo: c1.topAnchor),
            nameLabel.leadingAnchor.constraint(equalTo: c1.leadingAnchor),
            profileModeLabel.centerYAnchor.constraint(equalTo: nameLabel.centerYAnchor),
            profileModeLabel.trailingAnchor.constraint(equalTo: c1.trailingAnchor),
            profileModeLabel.leadingAnchor.constraint(greaterThanOrEqualTo: nameLabel.trailingAnchor, constant: 12),
            profileNameField.topAnchor.constraint(equalTo: nameLabel.bottomAnchor, constant: UI.labelGap),
            profileNameField.leadingAnchor.constraint(equalTo: c1.leadingAnchor),
            profileNameField.trailingAnchor.constraint(equalTo: c1.trailingAnchor),
            profileNameField.bottomAnchor.constraint(equalTo: c1.bottomAnchor),

            // Card 2 — Paste
            pasteCard.topAnchor.constraint(equalTo: profileCard.bottomAnchor, constant: UI.gap),
            pasteCard.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            pasteCard.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -UI.pad),
            pasteLabel.topAnchor.constraint(equalTo: c2.topAnchor),
            pasteLabel.leadingAnchor.constraint(equalTo: c2.leadingAnchor),
            pasteScroll.topAnchor.constraint(equalTo: pasteLabel.bottomAnchor, constant: UI.labelGap),
            pasteScroll.leadingAnchor.constraint(equalTo: c2.leadingAnchor),
            pasteScroll.trailingAnchor.constraint(equalTo: c2.trailingAnchor),
            pasteScroll.bottomAnchor.constraint(equalTo: c2.bottomAnchor),
            pasteScroll.heightAnchor.constraint(equalToConstant: 68),

            // Card 3 — Variables
            varsCard.topAnchor.constraint(equalTo: pasteCard.bottomAnchor, constant: UI.gap),
            varsCard.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            varsCard.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -UI.pad),
            varsCard.bottomAnchor.constraint(equalTo: statusLabel.topAnchor, constant: -UI.gap),
            variablesTitleLabel.topAnchor.constraint(equalTo: c3.topAnchor),
            variablesTitleLabel.leadingAnchor.constraint(equalTo: c3.leadingAnchor),
            variablesSummaryLabel.leadingAnchor.constraint(equalTo: variablesTitleLabel.trailingAnchor, constant: 8),
            variablesSummaryLabel.centerYAnchor.constraint(equalTo: variablesTitleLabel.centerYAnchor),
            variablesSummaryLabel.trailingAnchor.constraint(lessThanOrEqualTo: c3.trailingAnchor),
            fieldsScroll.topAnchor.constraint(equalTo: variablesTitleLabel.bottomAnchor, constant: 10),
            fieldsScroll.leadingAnchor.constraint(equalTo: c3.leadingAnchor),
            fieldsScroll.trailingAnchor.constraint(equalTo: c3.trailingAnchor),
            fieldsScroll.heightAnchor.constraint(greaterThanOrEqualToConstant: 150),
            editorButtons.topAnchor.constraint(equalTo: fieldsScroll.bottomAnchor, constant: 12),
            editorButtons.leadingAnchor.constraint(equalTo: c3.leadingAnchor),
            editorButtons.trailingAnchor.constraint(lessThanOrEqualTo: c3.trailingAnchor),
            editorButtons.bottomAnchor.constraint(equalTo: c3.bottomAnchor),

            // Footer
            saveButton.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -UI.pad),
            saveButton.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -UI.pad),
            saveButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 130),
            statusLabel.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            statusLabel.centerYAnchor.constraint(equalTo: saveButton.centerYAnchor),
            statusLabel.trailingAnchor.constraint(lessThanOrEqualTo: saveButton.leadingAnchor, constant: -12)
        ])

        clearDetailForNoSelection()
        return view
    }
}
