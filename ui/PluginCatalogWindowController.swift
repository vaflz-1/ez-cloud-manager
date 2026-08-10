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
    private var table: NSTableView!
    private var statusLabel: NSTextField!
    private var applyButton: NSButton!
    private var refreshGeneration = 0

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
        statusLabel.stringValue = "Refreshing workspace state…"
        service.runAsync({ try self.service.refreshProfile(id: self.profileID) }) { [weak self] result in
            guard let self, generation == self.refreshGeneration else { return }
            switch result {
            case .success:
                if self.draftEnabled == self.baselineEnabled {
                    self.reload()
                } else {
                    self.statusLabel.stringValue = "Workspace refreshed · local draft preserved"
                }
            case .failure(let error):
                self.statusLabel.stringValue = "Refresh failed: \(error.localizedDescription)"
            }
        }
    }

    private func reload() {
        do {
            descriptors = try service.listPlugins(profileID: profileID).filter { !$0.isSystem }
            baselineEnabled = Set(descriptors.filter(\.enabled).map(\.id))
            draftEnabled = baselineEnabled
            table.reloadData()
            updateApplyButton()
            if let profile = try? service.getProfile(id: profileID) {
                statusLabel.stringValue = "Last updated \(AppTimestamp.display(profile.updatedAt))"
            } else {
                statusLabel.stringValue = "Ready"
            }
        } catch {
            descriptors = []
            baselineEnabled = []
            draftEnabled = []
            table.reloadData()
            updateApplyButton()
            statusLabel.stringValue = "Error: \(error.localizedDescription)"
        }
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 460, height: 420),
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
        table.rowHeight = 56
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

        applyButton = NSButton(title: "Apply", target: self, action: #selector(applyChanges))
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
        cell.configure(descriptors[row], row: row, enabled: draftEnabled.contains(descriptors[row].id))
        return cell
    }

    @objc private func toggled(_ sender: NSButton) {
        let row = sender.tag
        guard row >= 0, row < descriptors.count else { return }
        let plugin = descriptors[row]
        if sender.state == .on {
            draftEnabled.insert(plugin.id)
        } else {
            draftEnabled.remove(plugin.id)
        }
        updateApplyButton()
        statusLabel.stringValue = "Unsaved add-on changes"
    }

    private func updateApplyButton() {
        applyButton?.isEnabled = draftEnabled != baselineEnabled
        window?.isDocumentEdited = draftEnabled != baselineEnabled
    }

    @objc private func applyChanges() {
        let changedIDs = baselineEnabled.symmetricDifference(draftEnabled)
        guard !changedIDs.isEmpty else { return }
        let submitted = draftEnabled
        let changes = Dictionary(uniqueKeysWithValues: changedIDs.map { ($0, submitted.contains($0)) })
        applyButton.isEnabled = false
        statusLabel.stringValue = "Saving…"
        service.runAsync({ try self.service.updatePlugins(profileID: self.profileID, changes: changes) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let saved):
                // The targeted core preserves unrelated concurrent add-on
                // changes. Recompose from the canonical saved Profile instead
                // of assuming our submitted draft is the entire enabled set.
                self.descriptors = (self.service.cachedPlugins(profileID: saved.id) ?? self.descriptors)
                    .filter { !$0.isSystem }
                self.baselineEnabled = Set(self.descriptors.filter(\.enabled).map(\.id))
                self.draftEnabled = self.baselineEnabled
                self.table.reloadData()
                self.updateApplyButton()
                self.statusLabel.stringValue = "Saved \(AppTimestamp.display(saved.updatedAt))"
                NotificationCenter.default.post(name: .profileDidChange, object: self.profileID)
            case .failure(let error):
                self.updateApplyButton()
                self.statusLabel.stringValue = "Save failed"
                let alert = NSAlert()
                alert.messageText = "Add-ons"
                alert.informativeText = error.localizedDescription
                alert.alertStyle = .warning
                alert.runModal()
            }
        }
    }
}

/// One catalog row: icon, name + description, category pill, enable checkbox.
final class PluginRowView: NSTableCellView {
    private let iconView = NSImageView()
    private let nameLabel = NSTextField(labelWithString: "")
    private let descriptionLabel = NSTextField(labelWithString: "")
    private let pillView = NSImageView()
    private let toggle = NSButton(checkboxWithTitle: "", target: nil, action: nil)

    init(reuseIdentifier: NSUserInterfaceItemIdentifier, target: AnyObject, action: Selector) {
        super.init(frame: .zero)
        identifier = reuseIdentifier

        iconView.translatesAutoresizingMaskIntoConstraints = false
        nameLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        descriptionLabel.font = .systemFont(ofSize: 11)
        descriptionLabel.textColor = .secondaryLabelColor
        descriptionLabel.lineBreakMode = .byTruncatingTail
        pillView.translatesAutoresizingMaskIntoConstraints = false
        toggle.target = target
        toggle.action = action
        toggle.setAccessibilityLabel("Enable add-on")
        toggle.translatesAutoresizingMaskIntoConstraints = false

        let textStack = NSStackView(views: [nameLabel, descriptionLabel])
        textStack.orientation = .vertical
        textStack.alignment = .leading
        textStack.spacing = 2
        textStack.translatesAutoresizingMaskIntoConstraints = false

        addSubview(iconView)
        addSubview(textStack)
        addSubview(pillView)
        addSubview(toggle)

        NSLayoutConstraint.activate([
            iconView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 8),
            iconView.centerYAnchor.constraint(equalTo: centerYAnchor),
            iconView.widthAnchor.constraint(equalToConstant: 24),
            iconView.heightAnchor.constraint(equalToConstant: 24),

            textStack.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: 10),
            textStack.centerYAnchor.constraint(equalTo: centerYAnchor),
            textStack.trailingAnchor.constraint(lessThanOrEqualTo: pillView.leadingAnchor, constant: -10),

            pillView.trailingAnchor.constraint(equalTo: toggle.leadingAnchor, constant: -12),
            pillView.centerYAnchor.constraint(equalTo: centerYAnchor),

            toggle.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -8),
            toggle.centerYAnchor.constraint(equalTo: centerYAnchor)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(_ plugin: PluginDescriptor, row: Int, enabled: Bool) {
        let cfg = NSImage.SymbolConfiguration(pointSize: 16, weight: .medium)
        iconView.image = NSImage(systemSymbolName: plugin.icon, accessibilityDescription: plugin.name)?
            .withSymbolConfiguration(cfg)
        iconView.contentTintColor = PluginStyle.color(plugin.category)
        nameLabel.stringValue = plugin.name
        descriptionLabel.stringValue = plugin.description
        pillView.image = PluginStyle.pill(plugin.category)
        toggle.tag = row
        toggle.state = enabled ? .on : .off
        toggle.setAccessibilityLabel("Enable \(plugin.name)")
    }
}
