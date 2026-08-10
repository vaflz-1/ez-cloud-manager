import AppKit

/// The Add-ons catalog sheet — every user-manageable registered add-on with
/// a checkbox reflecting whether this workspace has it enabled. Checkboxes
/// edit a local draft; Apply sends one
/// bulk patch, causing one profile write and one updatedAt change regardless
/// of how many addons were toggled.
final class PluginCatalogWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate {
    private let service: CredentialsService
    private let profileID: String
    private var descriptors: [PluginDescriptor] = []
    private var baselineEnabled: Set<String> = []
    private var draftEnabled: Set<String> = []
    private var workspaceName = "this workspace"
    private var workspaceUpdatedAt: String?
    private var table: NSTableView!
    private var statusLabel: NSTextField!
    private var applyButton: NSButton!
    private var refreshGeneration = 0
    private var isSaving = false

    init(service: CredentialsService, profileID: String) {
        self.service = service
        self.profileID = profileID
        super.init(window: nil)
        buildWindow()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    /// Presents as a sheet on `parent`, reloading the registry fresh every
    /// time — state is never cached across opens.
    func present(on parent: NSWindow) {
        reload()
        guard let win = window else { return }
        parent.beginSheet(win, completionHandler: nil)
        refreshFromDisk()
    }

    /// A catalog open is an explicit synchronization point with the headless
    /// CLI. It refreshes only this workspace, off the main thread, and never
    /// discards a toggle the user managed to make while the read was running.
    private func refreshFromDisk() {
        refreshGeneration += 1
        let generation = refreshGeneration
        setCatalogStatus("Refreshing workspace state…", announce: true)
        service.runAsync({ try self.service.refreshProfile(id: self.profileID) }) { [weak self] result in
            guard let self, generation == self.refreshGeneration else { return }
            switch result {
            case .success:
                if self.draftEnabled == self.baselineEnabled {
                    self.reload(announce: true)
                } else {
                    self.setCatalogStatus(
                        "Refreshed  ·  Draft preserved",
                        accessibility: "Workspace refreshed. Local add-on draft preserved.",
                        announce: true
                    )
                }
            case .failure(let error):
                self.setCatalogStatus(
                    "Refresh failed",
                    accessibility: "Refresh failed: \(error.localizedDescription)",
                    announce: true
                )
            }
        }
    }

    private func reload(announce: Bool = false) {
        do {
            descriptors = try service.listPlugins(profileID: profileID).filter { !$0.isSystem }
            baselineEnabled = Set(descriptors.filter(\.enabled).map(\.id))
            draftEnabled = baselineEnabled
            table.reloadData()
            updateApplyButton()
            if let profile = try? service.getProfile(id: profileID) {
                workspaceName = profile.name
                workspaceUpdatedAt = profile.updatedAt
                showReadyStatus(announce: announce)
            } else {
                workspaceUpdatedAt = nil
                setCatalogStatus(
                    "This workspace",
                    accessibility: "Changes apply to this workspace",
                    announce: announce
                )
            }
        } catch {
            descriptors = []
            baselineEnabled = []
            draftEnabled = []
            table.reloadData()
            updateApplyButton()
            setCatalogStatus(
                "Add-ons unavailable",
                accessibility: "Add-ons unavailable: \(error.localizedDescription)",
                announce: announce
            )
        }
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 500, height: 420),
            styleMask: [.titled, .closable],
            backing: .buffered, defer: false)
        win.title = "Add-ons"
        self.window = win

        let content = NSView()
        win.contentView = content

        table = NSTableView()
        table.headerView = nil
        table.delegate = self
        table.dataSource = self
        table.backgroundColor = .clear
        table.rowHeight = 60
        table.addTableColumn(NSTableColumn(identifier: NSUserInterfaceItemIdentifier("plugin")))

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        scroll.documentView = table
        scroll.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(scroll)

        statusLabel = NSTextField(labelWithString: "Ready")
        statusLabel.font = .systemFont(ofSize: 11)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(statusLabel)

        let cancelButton = NSButton(title: "Cancel", target: self, action: #selector(dismiss))
        cancelButton.bezelStyle = .rounded
        cancelButton.keyEquivalent = "\u{1b}"
        cancelButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(cancelButton)

        applyButton = NSButton(title: "Save Changes", target: self, action: #selector(applyChanges))
        applyButton.bezelStyle = .rounded
        applyButton.keyEquivalent = "\r"
        applyButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(applyButton)

        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: content.topAnchor, constant: 14),
            scroll.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            scroll.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            scroll.bottomAnchor.constraint(equalTo: statusLabel.topAnchor, constant: -12),

            statusLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            statusLabel.centerYAnchor.constraint(equalTo: applyButton.centerYAnchor),
            statusLabel.trailingAnchor.constraint(lessThanOrEqualTo: cancelButton.leadingAnchor, constant: -12),
            cancelButton.trailingAnchor.constraint(equalTo: applyButton.leadingAnchor, constant: -8),
            cancelButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14),
            applyButton.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            applyButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14)
        ])
    }

    @objc private func dismiss() {
        refreshGeneration += 1
        guard let win = window, let parent = win.sheetParent else { window?.close(); return }
        parent.endSheet(win)
    }

    // MARK: - Table

    func numberOfRows(in tableView: NSTableView) -> Int { descriptors.count }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row < descriptors.count else { return nil }
        let id = NSUserInterfaceItemIdentifier("pluginRow")
        let cell = (tableView.makeView(withIdentifier: id, owner: self) as? PluginRowView)
            ?? PluginRowView(reuseIdentifier: id, target: self, action: #selector(toggled(_:)))
        cell.configure(
            descriptors[row],
            row: row,
            enabled: draftEnabled.contains(descriptors[row].id),
            interactive: !isSaving
        )
        return cell
    }

    @objc private func toggled(_ sender: NSButton) {
        guard !isSaving else {
            table.reloadData()
            return
        }
        let row = sender.tag
        guard row >= 0, row < descriptors.count else { return }
        let plugin = descriptors[row]
        if sender.state == .on {
            draftEnabled.insert(plugin.id)
        } else {
            draftEnabled.remove(plugin.id)
        }
        updateApplyButton()
        if draftEnabled == baselineEnabled {
            showReadyStatus()
        } else {
            setCatalogStatus(
                "Unsaved changes  ·  “\(workspaceName)”",
                accessibility: "Unsaved add-on changes for workspace \(workspaceName)"
            )
        }
    }

    private func updateApplyButton() {
        applyButton?.isEnabled = !isSaving && draftEnabled != baselineEnabled
        window?.isDocumentEdited = draftEnabled != baselineEnabled
    }

    private func showReadyStatus(announce: Bool = false) {
        if let workspaceUpdatedAt {
            let updated = AppTimestamp.display(workspaceUpdatedAt)
            setCatalogStatus(
                "Updated \(updated)  ·  “\(workspaceName)”",
                accessibility: "Changes apply to workspace \(workspaceName). Last updated \(updated).",
                announce: announce
            )
        } else {
            setCatalogStatus(
                "“\(workspaceName)”",
                accessibility: "Changes apply to workspace \(workspaceName)",
                announce: announce
            )
        }
    }

    private func setCatalogStatus(
        _ visual: String,
        accessibility: String? = nil,
        announce: Bool = false
    ) {
        let full = accessibility ?? visual
        statusLabel.stringValue = visual
        statusLabel.toolTip = full
        statusLabel.setAccessibilityLabel(full)
        if announce, window?.isVisible == true {
            NSAccessibility.post(
                element: statusLabel as Any,
                notification: .announcementRequested,
                userInfo: [
                    .announcement: full,
                    .priority: NSAccessibilityPriorityLevel.medium.rawValue
                ]
            )
        }
    }

    @objc private func applyChanges() {
        let changedIDs = baselineEnabled.symmetricDifference(draftEnabled)
        guard !changedIDs.isEmpty else { return }
        let submitted = draftEnabled
        let changes = Dictionary(uniqueKeysWithValues: changedIDs.map { ($0, submitted.contains($0)) })
        isSaving = true
        updateApplyButton()
        table.reloadData()
        setCatalogStatus("Saving…", announce: true)
        service.runAsync({ try self.service.updatePlugins(profileID: self.profileID, changes: changes) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let saved):
                self.isSaving = false
                // The targeted core preserves unrelated concurrent add-on
                // changes. Recompose from the canonical saved Profile instead
                // of assuming our submitted draft is the entire enabled set.
                self.descriptors = (self.service.cachedPlugins(profileID: saved.id) ?? self.descriptors)
                    .filter { !$0.isSystem }
                self.baselineEnabled = Set(self.descriptors.filter(\.enabled).map(\.id))
                self.draftEnabled = self.baselineEnabled
                self.table.reloadData()
                self.updateApplyButton()
                self.workspaceUpdatedAt = saved.updatedAt
                self.showReadyStatus(announce: true)
                NotificationCenter.default.post(name: .profileDidChange, object: self.profileID)
            case .failure(let error):
                self.isSaving = false
                self.updateApplyButton()
                self.table.reloadData()
                self.setCatalogStatus(
                    "Save failed",
                    accessibility: "Save failed: \(error.localizedDescription)",
                    announce: true
                )
                let alert = NSAlert()
                alert.messageText = "Add-ons"
                alert.informativeText = error.localizedDescription
                alert.alertStyle = .warning
                alert.runModal()
            }
        }
    }
}

/// One catalog row: neutral add-on symbol, purpose, connector requirement and
/// an explicit draft checkbox. Marketplace categories stay out of the main
/// hierarchy until they carry a real filtering or policy meaning.
final class PluginRowView: NSTableCellView {
    private let iconView = NSImageView()
    private let nameLabel = NSTextField(labelWithString: "")
    private let descriptionLabel = NSTextField(labelWithString: "")
    private let connectorsLabel = NSTextField(labelWithString: "")
    private let toggle = NSButton(checkboxWithTitle: "", target: nil, action: nil)

    init(reuseIdentifier: NSUserInterfaceItemIdentifier, target: AnyObject, action: Selector) {
        super.init(frame: .zero)
        identifier = reuseIdentifier

        iconView.translatesAutoresizingMaskIntoConstraints = false
        iconView.setAccessibilityElement(false)
        nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        descriptionLabel.font = .systemFont(ofSize: 11)
        descriptionLabel.textColor = .secondaryLabelColor
        descriptionLabel.lineBreakMode = .byTruncatingTail
        connectorsLabel.font = UI.captionFont
        connectorsLabel.textColor = .tertiaryLabelColor
        connectorsLabel.lineBreakMode = .byTruncatingTail
        toggle.target = target
        toggle.action = action
        toggle.setAccessibilityLabel("Enable add-on")
        toggle.translatesAutoresizingMaskIntoConstraints = false

        let textStack = NSStackView(views: [nameLabel, descriptionLabel, connectorsLabel])
        textStack.orientation = .vertical
        textStack.alignment = .leading
        textStack.spacing = 2
        textStack.translatesAutoresizingMaskIntoConstraints = false

        addSubview(iconView)
        addSubview(textStack)
        addSubview(toggle)

        NSLayoutConstraint.activate([
            iconView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            iconView.centerYAnchor.constraint(equalTo: centerYAnchor),
            iconView.widthAnchor.constraint(equalToConstant: 24),
            iconView.heightAnchor.constraint(equalToConstant: 24),

            textStack.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: 10),
            textStack.centerYAnchor.constraint(equalTo: centerYAnchor),
            textStack.trailingAnchor.constraint(lessThanOrEqualTo: toggle.leadingAnchor, constant: -12),

            toggle.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            toggle.centerYAnchor.constraint(equalTo: centerYAnchor)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(_ plugin: PluginDescriptor, row: Int, enabled: Bool, interactive: Bool) {
        let cfg = NSImage.SymbolConfiguration(pointSize: 16, weight: .medium)
        iconView.image = NSImage(systemSymbolName: plugin.icon, accessibilityDescription: nil)?
            .withSymbolConfiguration(cfg)
        iconView.contentTintColor = .secondaryLabelColor
        nameLabel.stringValue = plugin.name
        descriptionLabel.stringValue = plugin.description
        if plugin.clouds.isEmpty {
            connectorsLabel.stringValue = "No connection required"
        } else {
            connectorsLabel.stringValue = "Requires \(plugin.clouds.map { $0.uppercased() }.joined(separator: " · "))"
        }
        toggle.tag = row
        toggle.state = enabled ? .on : .off
        toggle.isEnabled = interactive
        toggle.setAccessibilityLabel("Enable \(plugin.name)")
    }
}
