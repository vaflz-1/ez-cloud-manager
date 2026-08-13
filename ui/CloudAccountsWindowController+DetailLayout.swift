import AppKit

/// The detail pane (Cards 1–3 + footer) — split out of +Layout.swift to stay
/// under the file-size budget once the connectivity row and paste/variables
/// polish landed. +Layout.swift keeps window/toolbar/sidebar construction.
extension CloudAccountsWindowController {
    func buildDetail() -> NSView {
        let view = NSView()
        view.translatesAutoresizingMaskIntoConstraints = false

        // ── Card 1 · Connection name + connector + connectivity ─────────────
        let profileCard = makeCard()
        view.addSubview(profileCard)
        let c1 = profileCard.contentView!

        let nameLabel = sectionCaption("CONNECTION")
        c1.addSubview(nameLabel)

        providerPopup = NSPopUpButton()
        providerPopup.controlSize = .small
        providerPopup.font = .systemFont(ofSize: 11)
        providerPopup.target = self
        providerPopup.action = #selector(providerPopupChanged(_:))
        providerPopup.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(providerPopup)

        profileModeLabel = NSTextField(labelWithString: "New connection")
        profileModeLabel.font = .systemFont(ofSize: 11, weight: .medium)
        profileModeLabel.textColor = .secondaryLabelColor
        profileModeLabel.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(profileModeLabel)

        profileNameField = NSTextField()
        profileNameField.placeholderString = "example-connection"
        profileNameField.controlSize = .large
        profileNameField.delegate = self
        profileNameField.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(profileNameField)

        // Connectivity row: Test Connection + a result view whose state
        // (idle/testing/success/failure) is entirely managed in
        // CloudAccountsWindowController+Diagnostics.swift. An NSStackView so
        // each state's views collapse cleanly rather than leaving a gap or
        // needing manual show/hide constraint juggling.
        testConnectionButton = NSButton(title: "Test Connection", target: self, action: #selector(testConnection))
        testConnectionButton.image = NSImage(systemSymbolName: "checkmark.seal", accessibilityDescription: nil)
        testConnectionButton.imagePosition = .imageLeading
        testConnectionButton.bezelStyle = .rounded
        testConnectionButton.controlSize = .small
        testConnectionButton.font = .systemFont(ofSize: 11)
        testConnectionButton.isEnabled = false
        testConnectionButton.toolTip = "Select a saved connection to test its credentials"

        testConnectionSpinner = NSProgressIndicator()
        testConnectionSpinner.style = .spinning
        testConnectionSpinner.controlSize = .small
        testConnectionSpinner.isDisplayedWhenStopped = false
        NSLayoutConstraint.activate([
            testConnectionSpinner.widthAnchor.constraint(equalToConstant: 16),
            testConnectionSpinner.heightAnchor.constraint(equalToConstant: 16)
        ])

        testConnectionResultIcon = NSImageView()
        NSLayoutConstraint.activate([
            testConnectionResultIcon.widthAnchor.constraint(equalToConstant: 14),
            testConnectionResultIcon.heightAnchor.constraint(equalToConstant: 14)
        ])

        testConnectionResultLabel = NSTextField(labelWithString: "")
        testConnectionResultLabel.font = .systemFont(ofSize: 11)
        testConnectionResultLabel.textColor = .secondaryLabelColor
        testConnectionResultLabel.lineBreakMode = .byTruncatingTail

        testConnectionDetailsButton = NSButton(title: "Details", target: self, action: #selector(showConnectionErrorDetails(_:)))
        testConnectionDetailsButton.bezelStyle = .inline
        testConnectionDetailsButton.isBordered = false
        testConnectionDetailsButton.font = .systemFont(ofSize: 10, weight: .medium)
        testConnectionDetailsButton.contentTintColor = .linkColor
        testConnectionDetailsButton.isHidden = true

        let connectionStack = NSStackView(views: [
            testConnectionButton, testConnectionSpinner, testConnectionResultIcon,
            testConnectionResultLabel, testConnectionDetailsButton
        ])
        connectionStack.orientation = .horizontal
        connectionStack.alignment = .centerY
        connectionStack.spacing = 8
        connectionStack.translatesAutoresizingMaskIntoConstraints = false
        c1.addSubview(connectionStack)
        resetConnectionTestState()

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

        // NSTextView has no native placeholder — overlay a faint hint that
        // hides the moment there's any text (see textDidChange in +State.swift).
        pasteViewPlaceholder = NSTextField(labelWithString:
            "Paste `aws configure` output, a credentials block, or use Import File…")
        pasteViewPlaceholder.font = .systemFont(ofSize: 12)
        pasteViewPlaceholder.textColor = .tertiaryLabelColor
        pasteViewPlaceholder.isSelectable = false
        pasteViewPlaceholder.translatesAutoresizingMaskIntoConstraints = false
        c2.addSubview(pasteViewPlaceholder)
        NSLayoutConstraint.activate([
            pasteViewPlaceholder.leadingAnchor.constraint(equalTo: pasteScroll.leadingAnchor, constant: 8),
            pasteViewPlaceholder.topAnchor.constraint(equalTo: pasteScroll.topAnchor, constant: 6),
            pasteViewPlaceholder.trailingAnchor.constraint(lessThanOrEqualTo: pasteScroll.trailingAnchor, constant: -8)
        ])

        // ── Card 3 · Variables ──────────────────────────────────────────────
        let varsCard = makeCard()
        view.addSubview(varsCard)
        let c3 = varsCard.contentView!

        variablesTitleLabel = sectionCaption("VARIABLES")
        c3.addSubview(variablesTitleLabel)

        variablesSummaryLabel = NSTextField(labelWithString: "No connection variables loaded")
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
        // This is an editable form, not a selectable result list. The native
        // blue row highlight competes with field focus and obscures secrets.
        fieldsTable.selectionHighlightStyle = .none
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

        // Centered hint shown only while every row is empty (see
        // updateVariablesSummary in +State.swift) — an all-empty grid
        // otherwise looks broken rather than "add one".
        fieldsEmptyHintLabel = NSTextField(labelWithString: "No variables yet — click Add, or paste credentials above.")
        fieldsEmptyHintLabel.font = .systemFont(ofSize: 12)
        fieldsEmptyHintLabel.textColor = .tertiaryLabelColor
        fieldsEmptyHintLabel.isHidden = true
        fieldsEmptyHintLabel.translatesAutoresizingMaskIntoConstraints = false
        c3.addSubview(fieldsEmptyHintLabel)
        NSLayoutConstraint.activate([
            fieldsEmptyHintLabel.centerXAnchor.constraint(equalTo: fieldsScroll.centerXAnchor),
            fieldsEmptyHintLabel.centerYAnchor.constraint(equalTo: fieldsScroll.centerYAnchor, constant: -20)
        ])

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

        saveButton = NSButton(title: "Save Connection", target: self, action: #selector(saveProfile))
        saveButton.bezelStyle = .push
        saveButton.controlSize = .large
        saveButton.keyEquivalent = "\r"
        saveButton.translatesAutoresizingMaskIntoConstraints = false

        // A stack for the footer's action buttons — the same "structurally
        // impossible to overlap" idiom used everywhere else buttons sit in a
        // row, rather than a hand-written leading/trailing chain. Test
        // Connection moved into Card 1 (see above) — the footer is just the
        // save/activate actions now.
        let footerButtons = NSStackView(views: [activateButton, saveButton])
        footerButtons.orientation = .horizontal
        footerButtons.spacing = 10
        footerButtons.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(footerButtons)

        NSLayoutConstraint.activate([
            // Card 1 — Profile
            profileCard.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: UI.pad),
            profileCard.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            profileCard.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -UI.pad),
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

            connectionStack.topAnchor.constraint(equalTo: profileNameField.bottomAnchor, constant: 10),
            connectionStack.leadingAnchor.constraint(equalTo: c1.leadingAnchor),
            connectionStack.trailingAnchor.constraint(lessThanOrEqualTo: c1.trailingAnchor),
            connectionStack.bottomAnchor.constraint(equalTo: c1.bottomAnchor),

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
            footerButtons.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -UI.pad),
            footerButtons.bottomAnchor.constraint(equalTo: view.bottomAnchor, constant: -UI.pad),
            saveButton.widthAnchor.constraint(greaterThanOrEqualToConstant: 130),
            statusLabel.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: UI.pad),
            statusLabel.centerYAnchor.constraint(equalTo: footerButtons.centerYAnchor),
            statusLabel.trailingAnchor.constraint(lessThanOrEqualTo: footerButtons.leadingAnchor, constant: -12)
        ])

        clearDetailForNoSelection()
        return view
    }
}
