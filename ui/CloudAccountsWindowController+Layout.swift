import AppKit

extension CloudAccountsWindowController {
    func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1180, height: 820),
            styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        win.title = "EZ Cloud Manager — \(profile.name)"
        win.minSize = NSSize(width: 920, height: 640)
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
        [.toggleSidebar, .flexibleSpace, .init("refresh")]
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
        profilesTable.floatsGroupRows = false
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

        // ── Card 1 · Profile name + provider ────────────────────────────────
        let profileCard = makeCard()
        view.addSubview(profileCard)
        let c1 = profileCard.contentView!

        // Leading accent stripe in the editing provider's brand color — ties
        // the detail pane back to the sidebar's color coding at a glance.
        profileCardStripe = NSView()
        profileCardStripe.wantsLayer = true
        profileCardStripe.layer?.cornerRadius = 1.5
        profileCardStripe.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(profileCardStripe)

        let nameLabel = sectionCaption("PROFILE")
        c1.addSubview(nameLabel)

        providerPopup = NSPopUpButton()
        providerPopup.controlSize = .small
        providerPopup.font = .systemFont(ofSize: 11)
        providerPopup.target = self
        providerPopup.action = #selector(providerPopupChanged(_:))
        providerPopup.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(providerPopup)

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

        pasteLabel = sectionCaption("PASTE AWS CREDENTIALS OR CONFIG")
        c2.addSubview(pasteLabel)

        let importButton = roundedButton(title: "Import File…", systemImage: "square.and.arrow.down", action: #selector(importFromFile))
        importButton.controlSize = .small
        importButton.font = .systemFont(ofSize: 11)
        importButton.translatesAutoresizingMaskIntoConstraints = false
        c2.addSubview(importButton)

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
        let compareButton = roundedButton(title: "Compare…", systemImage: "arrow.left.arrow.right", action: #selector(compareProfiles))

        // Export is a pull-down: formats target the clipboard (concealed),
        // "Save to File…" writes wherever the user picks.
        exportButton = NSPopUpButton()
        exportButton.pullsDown = true
        exportButton.addItem(withTitle: "Export")
        for (title, tag) in [("Copy as shell exports", 0), ("Copy as .env", 1), ("Copy as INI", 2), ("Copy as JSON", 3)] {
            let item = NSMenuItem(title: title, action: #selector(exportTapped(_:)), keyEquivalent: "")
            item.target = self
            item.tag = tag
            exportButton.menu?.addItem(item)
        }
        exportButton.menu?.addItem(.separator())
        let saveToFile = NSMenuItem(title: "Save to File…", action: #selector(exportToFile), keyEquivalent: "")
        saveToFile.target = self
        exportButton.menu?.addItem(saveToFile)
        exportButton.translatesAutoresizingMaskIntoConstraints = false

        let editorButtons = NSStackView(views: [addVariableButton, removeVariableButton, copyValueButton, compareButton, exportButton])
        editorButtons.orientation = .horizontal
        editorButtons.spacing = 8
        editorButtons.translatesAutoresizingMaskIntoConstraints = false
        c3.addSubview(editorButtons)

        // ── Footer · status + activate + primary action ─────────────────────
        statusLabel = NSTextField(labelWithString: "Ready")
        statusLabel.font = .systemFont(ofSize: 11, weight: .regular)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(statusLabel)

        activateButton = NSButton(title: "Set Active", target: self, action: #selector(activateProfile))
        activateButton.bezelStyle = .rounded
        activateButton.controlSize = .large
        activateButton.isHidden = true
        activateButton.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(activateButton)

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
            profileCardStripe.leadingAnchor.constraint(equalTo: c1.leadingAnchor, constant: -UI.cardPad + 4),
            profileCardStripe.topAnchor.constraint(equalTo: c1.topAnchor, constant: -4),
            profileCardStripe.bottomAnchor.constraint(equalTo: c1.bottomAnchor, constant: 4),
            profileCardStripe.widthAnchor.constraint(equalToConstant: 3),
            nameLabel.topAnchor.constraint(equalTo: c1.topAnchor),
            nameLabel.leadingAnchor.constraint(equalTo: c1.leadingAnchor),
            providerPopup.centerYAnchor.constraint(equalTo: nameLabel.centerYAnchor),
            providerPopup.leadingAnchor.constraint(equalTo: nameLabel.trailingAnchor, constant: 10),
            profileModeLabel.centerYAnchor.constraint(equalTo: nameLabel.centerYAnchor),
            profileModeLabel.trailingAnchor.constraint(equalTo: c1.trailingAnchor),
            profileModeLabel.leadingAnchor.constraint(greaterThanOrEqualTo: providerPopup.trailingAnchor, constant: 12),
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
            importButton.centerYAnchor.constraint(equalTo: pasteLabel.centerYAnchor),
            importButton.trailingAnchor.constraint(equalTo: c2.trailingAnchor),
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
            activateButton.trailingAnchor.constraint(equalTo: saveButton.leadingAnchor, constant: -10),
            activateButton.centerYAnchor.constraint(equalTo: saveButton.centerYAnchor),
            statusLabel.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            statusLabel.centerYAnchor.constraint(equalTo: saveButton.centerYAnchor),
            statusLabel.trailingAnchor.constraint(lessThanOrEqualTo: activateButton.leadingAnchor, constant: -12)
        ])

        clearDetailForNoSelection()
        return view
    }
}
