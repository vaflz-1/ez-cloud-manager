import AppKit

/// The "Add Plugins" catalog sheet — every registered plugin
/// (internal/plugin.Builtins, unfiltered) with a switch reflecting whether
/// THIS profile has it enabled. Toggling calls `ezcloud plugins
/// enable|disable` immediately (no separate "Apply"); success reloads this
/// sheet, calls `onChange` so the Hub's grid catches up, and posts
/// `.profileDidChange` so any OTHER open window on the same profile
/// (Profile Manager, another Hub) refreshes too — reusing P0's existing
/// cross-window notification exactly as ProfileManagerWindowController's
/// saveDetail() already does.
final class PluginCatalogWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate {
    private let service: CredentialsService
    private let profileID: String
    private var descriptors: [PluginDescriptor] = []
    private var table: NSTableView!

    /// Called after any successful enable/disable — lets the Hub refresh its
    /// grid without a direct reference back into this window.
    var onChange: (() -> Void)?

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
    }

    private func reload() {
        descriptors = (try? service.listPlugins(profileID: profileID)) ?? []
        table.reloadData()
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 460, height: 420),
            styleMask: [.titled, .closable],
            backing: .buffered, defer: false)
        win.title = "Add Plugins"
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

        let doneButton = NSButton(title: "Done", target: self, action: #selector(dismiss))
        doneButton.bezelStyle = .rounded
        doneButton.keyEquivalent = "\r"
        doneButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(doneButton)

        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: content.topAnchor, constant: 14),
            scroll.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 14),
            scroll.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            scroll.bottomAnchor.constraint(equalTo: doneButton.topAnchor, constant: -12),

            doneButton.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -14),
            doneButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -14)
        ])
    }

    @objc private func dismiss() {
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
        cell.configure(descriptors[row], row: row)
        return cell
    }

    @objc private func toggled(_ sender: NSSwitch) {
        let row = sender.tag
        guard row >= 0, row < descriptors.count else { return }
        let plugin = descriptors[row]
        let enable = sender.state == .on
        service.runAsync({
            if enable {
                return try self.service.enablePlugin(profileID: self.profileID, pluginID: plugin.id)
            } else {
                return try self.service.disablePlugin(profileID: self.profileID, pluginID: plugin.id)
            }
        }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success:
                self.reload()
                self.onChange?()
                NotificationCenter.default.post(name: .profileDidChange, object: self.profileID)
            case .failure(let error):
                sender.state = enable ? .off : .on // revert the switch
                let alert = NSAlert()
                alert.messageText = "Add Plugins"
                alert.informativeText = error.localizedDescription
                alert.alertStyle = .warning
                alert.runModal()
            }
        }
    }
}

/// One catalog row: icon, name + description, category pill, enable switch.
final class PluginRowView: NSTableCellView {
    private let iconView = NSImageView()
    private let nameLabel = NSTextField(labelWithString: "")
    private let descriptionLabel = NSTextField(labelWithString: "")
    private let pillView = NSImageView()
    private let toggle = NSSwitch()

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

    func configure(_ plugin: PluginDescriptor, row: Int) {
        let cfg = NSImage.SymbolConfiguration(pointSize: 16, weight: .medium)
        iconView.image = NSImage(systemSymbolName: plugin.icon, accessibilityDescription: plugin.name)?
            .withSymbolConfiguration(cfg)
        iconView.contentTintColor = PluginStyle.color(plugin.category)
        nameLabel.stringValue = plugin.name
        descriptionLabel.stringValue = plugin.description
        pillView.image = PluginStyle.pill(plugin.category)
        toggle.tag = row
        toggle.state = plugin.enabled ? .on : .off
    }
}
