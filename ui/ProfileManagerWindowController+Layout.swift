import AppKit

/// Window construction for the Profile Manager, split out to keep the
/// controller file focused on behavior (and under the size budget).
///
/// P1.5: no Accounts section (that's the Cloud Accounts plugin's own Scope
/// sheet now). Export/Import/Duplicate/Open Window DO still live here, just
/// re-homed onto the sidebar's "⋯" pull-down menu instead of five
/// always-visible buttons — two segments (add/delete) + one menu button is
/// what makes the row structurally impossible to overflow, not removing
/// functionality. The detail pane adopts the same card system Cloud
/// Accounts uses (UI.makeCard()/UI.sectionCaption(_:), UI.pad/UI.gap) for
/// one-product visual consistency, with a third, read-only PLUGINS card
/// filling the space the removed Accounts section vacated.
extension ProfileManagerWindowController {
    func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 820, height: 560),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false)
        win.title = "Workspaces — \(Product.name)"
        win.minSize = NSSize(width: 700, height: 460)
        win.center()
        self.window = win

        let content = NSView()
        win.contentView = content

        // ── Left: profile list (sidebar material, matching Cloud Accounts) ──
        let sidebar = NSVisualEffectView()
        sidebar.material = .sidebar
        sidebar.blendingMode = .behindWindow
        sidebar.state = .followsWindowActiveState
        sidebar.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(sidebar)

        profilesTable = NSTableView()
        profilesTable.headerView = nil
        profilesTable.delegate = self
        profilesTable.dataSource = self
        profilesTable.style = .sourceList
        profilesTable.backgroundColor = .clear
        profilesTable.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("name")))

        let listScroll = NSScrollView()
        listScroll.hasVerticalScroller = true
        listScroll.drawsBackground = false
        listScroll.documentView = profilesTable
        listScroll.translatesAutoresizingMaskIntoConstraints = false
        sidebar.addSubview(listScroll)

        // Two segments (add/delete) + one "⋯" menu button — structurally
        // impossible to overflow a 220pt column, unlike the five separate
        // buttons an earlier draft of this window had.
        let addRemoveControl = NSSegmentedControl(
            images: [
                NSImage(systemSymbolName: "plus", accessibilityDescription: "New Workspace")!,
                NSImage(systemSymbolName: "minus", accessibilityDescription: "Delete Workspace")!
            ],
            trackingMode: .momentary, target: self, action: #selector(listSegmentChanged(_:)))
        addRemoveControl.segmentStyle = .smallSquare
        addRemoveControl.controlSize = .small
        addRemoveControl.translatesAutoresizingMaskIntoConstraints = false

        let moreMenu = NSPopUpButton()
        moreMenu.pullsDown = true
        moreMenu.bezelStyle = .smallSquare
        moreMenu.controlSize = .small
        moreMenu.addItem(withTitle: "")
        moreMenu.item(at: 0)?.image = NSImage(systemSymbolName: "ellipsis.circle", accessibilityDescription: "More")
        for (title, action) in [
            ("Duplicate", #selector(duplicateSelected)),
            ("Export…", #selector(exportSelected)),
            ("Import…", #selector(importFromFile))
        ] {
            let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
            item.target = self
            moreMenu.menu?.addItem(item)
        }
        moreMenu.menu?.addItem(.separator())
        let openItem = NSMenuItem(title: "Open Window", action: #selector(openWindowForSelected), keyEquivalent: "")
        openItem.target = self
        moreMenu.menu?.addItem(openItem)
        moreMenu.translatesAutoresizingMaskIntoConstraints = false

        sidebar.addSubview(addRemoveControl)
        sidebar.addSubview(moreMenu)

        // ── Right: detail editor — 3 cards ──────────────────────────────────
        let detail = NSView()
        detail.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(detail)

        // Card A — Name
        let nameCard = UI.makeCard()
        detail.addSubview(nameCard)
        let cName = nameCard.contentView!
        let nameLabel = UI.sectionCaption("NAME")
        cName.addSubview(nameLabel)
        nameField = NSTextField()
        nameField.delegate = self
        nameField.translatesAutoresizingMaskIntoConstraints = false
        cName.addSubview(nameField)
        NSLayoutConstraint.activate([
            nameLabel.topAnchor.constraint(equalTo: cName.topAnchor),
            nameLabel.leadingAnchor.constraint(equalTo: cName.leadingAnchor),
            nameField.topAnchor.constraint(equalTo: nameLabel.bottomAnchor, constant: UI.labelGap),
            nameField.leadingAnchor.constraint(equalTo: cName.leadingAnchor),
            nameField.trailingAnchor.constraint(equalTo: cName.trailingAnchor),
            nameField.bottomAnchor.constraint(equalTo: cName.bottomAnchor)
        ])

        // Card B — Env vars
        let envCard = UI.makeCard()
        detail.addSubview(envCard)
        let cEnv = envCard.contentView!
        let envLabel = UI.sectionCaption("ENV VARS")
        cEnv.addSubview(envLabel)
        let warningLabel = NSTextField(labelWithString: "Keep secrets in Connections or Keychain.")
        warningLabel.font = .systemFont(ofSize: 10, weight: .medium)
        warningLabel.textColor = .systemOrange
        warningLabel.lineBreakMode = .byTruncatingTail
        warningLabel.translatesAutoresizingMaskIntoConstraints = false
        cEnv.addSubview(warningLabel)

        envVarsTable = NSTableView()
        envVarsTable.delegate = self
        envVarsTable.dataSource = self
        envVarsTable.usesAlternatingRowBackgroundColors = true
        let keyCol = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("key"))
        keyCol.title = "Key"
        keyCol.width = 160
        let valueCol = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("value"))
        valueCol.title = "Value"
        valueCol.width = 260
        envVarsTable.addTableColumn(keyCol)
        envVarsTable.addTableColumn(valueCol)
        let envScroll = NSScrollView()
        envScroll.hasVerticalScroller = true
        envScroll.borderType = .bezelBorder
        envScroll.documentView = envVarsTable
        envScroll.translatesAutoresizingMaskIntoConstraints = false
        cEnv.addSubview(envScroll)

        // In-card segmented control (not free-floating buttons) — the exact
        // spot the old Add/Remove stack overlapped Export/Import in an
        // earlier draft.
        let envAddRemove = NSSegmentedControl(
            images: [
                NSImage(systemSymbolName: "plus", accessibilityDescription: "Add")!,
                NSImage(systemSymbolName: "minus", accessibilityDescription: "Remove")!
            ],
            trackingMode: .momentary, target: self, action: #selector(envSegmentChanged(_:)))
        envAddRemove.segmentStyle = .smallSquare
        envAddRemove.controlSize = .small
        envAddRemove.translatesAutoresizingMaskIntoConstraints = false
        cEnv.addSubview(envAddRemove)

        NSLayoutConstraint.activate([
            envLabel.topAnchor.constraint(equalTo: cEnv.topAnchor),
            envLabel.leadingAnchor.constraint(equalTo: cEnv.leadingAnchor),
            warningLabel.centerYAnchor.constraint(equalTo: envLabel.centerYAnchor),
            warningLabel.trailingAnchor.constraint(equalTo: cEnv.trailingAnchor),
            warningLabel.leadingAnchor.constraint(greaterThanOrEqualTo: envLabel.trailingAnchor, constant: 12),
            envScroll.topAnchor.constraint(equalTo: envLabel.bottomAnchor, constant: UI.labelGap),
            envScroll.leadingAnchor.constraint(equalTo: cEnv.leadingAnchor),
            envScroll.trailingAnchor.constraint(equalTo: cEnv.trailingAnchor),
            envScroll.heightAnchor.constraint(equalToConstant: 130),
            envAddRemove.topAnchor.constraint(equalTo: envScroll.bottomAnchor, constant: 8),
            envAddRemove.leadingAnchor.constraint(equalTo: cEnv.leadingAnchor),
            envAddRemove.bottomAnchor.constraint(equalTo: cEnv.bottomAnchor)
        ])

        // Card C — Plugins (read-only summary)
        let pluginsCard = UI.makeCard()
        detail.addSubview(pluginsCard)
        let cPlugins = pluginsCard.contentView!
        let pluginsLabel = UI.sectionCaption("ADD-ONS")
        cPlugins.addSubview(pluginsLabel)

        pluginsEmptyLabel = NSTextField(labelWithString: "No add-ons enabled — browse them from the workspace.")
        pluginsEmptyLabel.font = .systemFont(ofSize: 11)
        pluginsEmptyLabel.textColor = .tertiaryLabelColor
        pluginsEmptyLabel.translatesAutoresizingMaskIntoConstraints = false
        cPlugins.addSubview(pluginsEmptyLabel)

        pluginsStack = NSStackView()
        pluginsStack.orientation = .horizontal
        pluginsStack.spacing = 16
        pluginsStack.translatesAutoresizingMaskIntoConstraints = false
        cPlugins.addSubview(pluginsStack)

        NSLayoutConstraint.activate([
            pluginsLabel.topAnchor.constraint(equalTo: cPlugins.topAnchor),
            pluginsLabel.leadingAnchor.constraint(equalTo: cPlugins.leadingAnchor),
            pluginsEmptyLabel.topAnchor.constraint(equalTo: pluginsLabel.bottomAnchor, constant: UI.labelGap),
            pluginsEmptyLabel.leadingAnchor.constraint(equalTo: cPlugins.leadingAnchor),
            pluginsEmptyLabel.bottomAnchor.constraint(equalTo: cPlugins.bottomAnchor),
            pluginsStack.topAnchor.constraint(equalTo: pluginsLabel.bottomAnchor, constant: UI.labelGap),
            pluginsStack.leadingAnchor.constraint(equalTo: cPlugins.leadingAnchor),
            pluginsStack.trailingAnchor.constraint(lessThanOrEqualTo: cPlugins.trailingAnchor),
            pluginsStack.bottomAnchor.constraint(equalTo: cPlugins.bottomAnchor)
        ])

        // Footer
        statusLabel = NSTextField(labelWithString: "Ready")
        statusLabel.font = .systemFont(ofSize: 11)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(statusLabel)

        saveButton = NSButton(title: "Save", target: self, action: #selector(saveDetail))
        UI.style(saveButton, as: .primary)
        saveButton.keyEquivalent = "\r"
        saveButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(saveButton)

        NSLayoutConstraint.activate([
            sidebar.topAnchor.constraint(equalTo: content.topAnchor),
            sidebar.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            sidebar.bottomAnchor.constraint(equalTo: content.bottomAnchor),
            sidebar.widthAnchor.constraint(equalToConstant: 220),

            listScroll.topAnchor.constraint(equalTo: sidebar.topAnchor, constant: UI.labelGap),
            listScroll.leadingAnchor.constraint(equalTo: sidebar.leadingAnchor, constant: 4),
            listScroll.trailingAnchor.constraint(equalTo: sidebar.trailingAnchor, constant: -4),
            listScroll.bottomAnchor.constraint(equalTo: addRemoveControl.topAnchor, constant: -UI.labelGap),

            addRemoveControl.leadingAnchor.constraint(equalTo: sidebar.leadingAnchor, constant: UI.labelGap),
            addRemoveControl.bottomAnchor.constraint(equalTo: sidebar.bottomAnchor, constant: -UI.labelGap),
            moreMenu.leadingAnchor.constraint(equalTo: addRemoveControl.trailingAnchor, constant: 8),
            moreMenu.centerYAnchor.constraint(equalTo: addRemoveControl.centerYAnchor),
            moreMenu.trailingAnchor.constraint(lessThanOrEqualTo: sidebar.trailingAnchor, constant: -UI.labelGap),

            detail.topAnchor.constraint(equalTo: content.topAnchor, constant: UI.pad),
            detail.leadingAnchor.constraint(equalTo: sidebar.trailingAnchor, constant: UI.gap),
            detail.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -UI.pad),
            detail.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -UI.pad),

            nameCard.topAnchor.constraint(equalTo: detail.topAnchor),
            nameCard.leadingAnchor.constraint(equalTo: detail.leadingAnchor),
            nameCard.trailingAnchor.constraint(equalTo: detail.trailingAnchor),

            envCard.topAnchor.constraint(equalTo: nameCard.bottomAnchor, constant: UI.gap),
            envCard.leadingAnchor.constraint(equalTo: detail.leadingAnchor),
            envCard.trailingAnchor.constraint(equalTo: detail.trailingAnchor),

            pluginsCard.topAnchor.constraint(equalTo: envCard.bottomAnchor, constant: UI.gap),
            pluginsCard.leadingAnchor.constraint(equalTo: detail.leadingAnchor),
            pluginsCard.trailingAnchor.constraint(equalTo: detail.trailingAnchor),
            pluginsCard.bottomAnchor.constraint(equalTo: statusLabel.topAnchor, constant: -UI.gap),

            statusLabel.leadingAnchor.constraint(equalTo: detail.leadingAnchor),
            statusLabel.centerYAnchor.constraint(equalTo: saveButton.centerYAnchor),
            statusLabel.trailingAnchor.constraint(lessThanOrEqualTo: saveButton.leadingAnchor, constant: -12),
            saveButton.trailingAnchor.constraint(equalTo: detail.trailingAnchor),
            saveButton.bottomAnchor.constraint(equalTo: detail.bottomAnchor)
        ])
    }
}
