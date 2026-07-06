import AppKit

/// Window construction for the Profile Manager, split out to keep the
/// controller file focused on behavior (and under the size budget).
extension ProfileManagerWindowController {
    func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 780, height: 560),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false)
        win.title = "Manage Profiles"
        win.minSize = NSSize(width: 680, height: 460)
        win.center()
        self.window = win

        let content = NSView()
        win.contentView = content

        // ── Left: profile list + list-level actions ──────────────────────
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
        content.addSubview(listScroll)

        let newButton = smallButton("New…", action: #selector(newProfile))
        let dupButton = smallButton("Duplicate", action: #selector(duplicateSelected))
        let deleteButton = smallButton("Delete…", action: #selector(deleteSelected))
        let exportButton = smallButton("Export…", action: #selector(exportSelected))
        let importButton = smallButton("Import…", action: #selector(importFromFile))
        let openButton = smallButton("Open Window", action: #selector(openWindowForSelected))

        let listButtons = NSStackView(views: [newButton, dupButton, deleteButton, exportButton, importButton])
        listButtons.orientation = .horizontal
        listButtons.spacing = 6
        listButtons.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(listButtons)
        content.addSubview(openButton)

        // ── Right: detail editor ───────────────────────────────────────────
        let nameLabel = caption("NAME")
        content.addSubview(nameLabel)

        nameField = NSTextField()
        nameField.delegate = self
        nameField.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(nameField)

        showAllCheckbox = NSButton(checkboxWithTitle: "Show all accounts (ignore the list below)", target: nil, action: nil)
        showAllCheckbox.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(showAllCheckbox)

        let accountsLabel = caption("ACCOUNTS")
        content.addSubview(accountsLabel)

        accountsTable = NSTableView()
        accountsTable.headerView = nil
        accountsTable.delegate = self
        accountsTable.dataSource = self
        accountsTable.backgroundColor = .clear
        accountsTable.rowHeight = 22
        accountsTable.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("account")))
        let accountsScroll = NSScrollView()
        accountsScroll.hasVerticalScroller = true
        accountsScroll.borderType = .bezelBorder
        accountsScroll.documentView = accountsTable
        accountsScroll.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(accountsScroll)

        let envLabel = caption("ENV VARS")
        content.addSubview(envLabel)

        let warningLabel = NSTextField(labelWithString: "Don't store secrets here — use a cloud account profile or Keychain.")
        warningLabel.font = .systemFont(ofSize: 10, weight: .medium)
        warningLabel.textColor = .systemOrange
        warningLabel.lineBreakMode = .byTruncatingTail
        warningLabel.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(warningLabel)

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
        content.addSubview(envScroll)

        let addEnvButton = smallButton("Add", action: #selector(addEnvVar))
        let removeEnvButton = smallButton("Remove", action: #selector(removeEnvVar))
        let envButtons = NSStackView(views: [addEnvButton, removeEnvButton])
        envButtons.orientation = .horizontal
        envButtons.spacing = 6
        envButtons.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(envButtons)

        statusLabel = NSTextField(labelWithString: "Ready")
        statusLabel.font = .systemFont(ofSize: 11)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(statusLabel)

        saveButton = NSButton(title: "Save", target: self, action: #selector(saveDetail))
        saveButton.bezelStyle = .push
        saveButton.keyEquivalent = "\r"
        saveButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(saveButton)

        NSLayoutConstraint.activate([
            listScroll.topAnchor.constraint(equalTo: content.topAnchor, constant: 14),
            listScroll.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            listScroll.widthAnchor.constraint(equalToConstant: 200),
            listScroll.bottomAnchor.constraint(equalTo: listButtons.topAnchor, constant: -8),

            listButtons.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            listButtons.bottomAnchor.constraint(equalTo: openButton.topAnchor, constant: -6),
            openButton.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            openButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14),

            nameLabel.topAnchor.constraint(equalTo: content.topAnchor, constant: 14),
            nameLabel.leadingAnchor.constraint(equalTo: listScroll.trailingAnchor, constant: 20),
            nameField.topAnchor.constraint(equalTo: nameLabel.bottomAnchor, constant: 4),
            nameField.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            nameField.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),

            showAllCheckbox.topAnchor.constraint(equalTo: nameField.bottomAnchor, constant: 10),
            showAllCheckbox.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),

            accountsLabel.topAnchor.constraint(equalTo: showAllCheckbox.bottomAnchor, constant: 14),
            accountsLabel.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            accountsScroll.topAnchor.constraint(equalTo: accountsLabel.bottomAnchor, constant: 4),
            accountsScroll.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            accountsScroll.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            accountsScroll.heightAnchor.constraint(equalToConstant: 110),

            envLabel.topAnchor.constraint(equalTo: accountsScroll.bottomAnchor, constant: 14),
            envLabel.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            warningLabel.centerYAnchor.constraint(equalTo: envLabel.centerYAnchor),
            warningLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            warningLabel.leadingAnchor.constraint(greaterThanOrEqualTo: envLabel.trailingAnchor, constant: 12),
            envScroll.topAnchor.constraint(equalTo: envLabel.bottomAnchor, constant: 4),
            envScroll.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            envScroll.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            envScroll.bottomAnchor.constraint(equalTo: envButtons.topAnchor, constant: -8),

            envButtons.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            envButtons.bottomAnchor.constraint(equalTo: statusLabel.topAnchor, constant: -10),

            statusLabel.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            statusLabel.centerYAnchor.constraint(equalTo: saveButton.centerYAnchor),
            statusLabel.trailingAnchor.constraint(lessThanOrEqualTo: saveButton.leadingAnchor, constant: -12),
            saveButton.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            saveButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14)
        ])
    }

    private func smallButton(_ title: String, action: Selector) -> NSButton {
        let button = NSButton(title: title, target: self, action: action)
        button.bezelStyle = .rounded
        button.controlSize = .small
        button.font = .systemFont(ofSize: 11)
        button.translatesAutoresizingMaskIntoConstraints = false
        return button
    }

    private func caption(_ text: String) -> NSTextField {
        let label = NSTextField(labelWithString: text)
        label.font = .systemFont(ofSize: 11, weight: .semibold)
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        return label
    }
}
