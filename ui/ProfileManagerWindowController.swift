import AppKit

/// App-global, profile-list utility window — NOT bound to any single
/// profile (contrast ProfileWindowController). Provides profile CRUD
/// (create/rename-via-Save/duplicate/delete/export/import), a per-profile
/// Accounts membership editor and env var editor, and "Open Window" for any
/// profile.
///
/// This is the direct replacement for the old sidebar right-click "toggle
/// workspace membership" context menu, removed along with
/// AppDelegate+Workspaces.swift (docs/PLATFORM.md phase P0) — a disclosed
/// UX change, not a silent regression.
final class ProfileManagerWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate {
    let service: CredentialsService

    var profilesTable: NSTableView!
    var accountsTable: NSTableView!
    var envVarsTable: NSTableView!
    var nameField: NSTextField!
    var showAllCheckbox: NSButton!
    var statusLabel: NSTextField!
    var saveButton: NSButton!

    /// All known profiles, sorted by name — the left-hand list.
    var profiles: [Profile] = []
    /// The profile currently shown in the detail pane (a working copy;
    /// edits stay local until Save PUTs the whole object back).
    var editing: Profile?
    /// Every (provider, account) credential entry across all providers, for
    /// the membership checkboxes — reloaded each time the window is shown.
    var allAccounts: [AccountRef] = []

    init(service: CredentialsService) {
        self.service = service
        super.init(window: nil)
        buildWindow()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    /// Reloads everything and shows the window — called every time (state is
    /// never cached across opens) so edits made elsewhere are never stale.
    func show() {
        reloadAll()
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func reloadAll() {
        do {
            profiles = try service.listProfiles()
            allAccounts = try loadAllAccounts()
        } catch {
            setStatus("Error: \(error.localizedDescription)")
        }
        let keepID = editing?.id
        profilesTable.reloadData()
        if let keepID, let refreshed = profiles.first(where: { $0.id == keepID }) {
            select(refreshed, persistSelection: true)
        } else if let first = profiles.first {
            select(first, persistSelection: true)
        } else {
            editing = nil
            clearDetail()
        }
    }

    private func loadAllAccounts() throws -> [AccountRef] {
        var out: [AccountRef] = []
        for info in try service.providers() {
            let response = try service.list(provider: info.id)
            out += response.profiles.map { AccountRef(provider: info.id, account: $0.name) }
        }
        return out
    }

    private func select(_ profile: Profile, persistSelection: Bool = false) {
        editing = profile
        nameField.stringValue = profile.name
        showAllCheckbox.state = profile.showAllAccounts ? .on : .off
        accountsTable.reloadData()
        envVarsTable.reloadData()
        updateSaveButton()
        if persistSelection, let idx = profiles.firstIndex(where: { $0.id == profile.id }) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        }
    }

    private func clearDetail() {
        nameField.stringValue = ""
        showAllCheckbox.state = .off
        accountsTable.reloadData()
        envVarsTable.reloadData()
        updateSaveButton()
    }

    private func setStatus(_ message: String) {
        statusLabel.stringValue = message
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "Manage Profiles"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }

    private func updateSaveButton() {
        saveButton.isEnabled = editing != nil
    }

    // MARK: - Profile list actions

    @objc func newProfile() {
        let alert = NSAlert()
        alert.messageText = "New profile"
        alert.informativeText = "A profile is a global container: cloud account references, env vars, and (later) plugins/settings. Each app window binds to exactly one."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        field.placeholderString = "e.g. acme-prod"
        alert.accessoryView = field
        alert.addButton(withTitle: "Create")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let name = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return }
        do {
            let created = try service.createProfile(name: name)
            reloadAll()
            select(created, persistSelection: true)
            setStatus("Created \(created.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func duplicateSelected() {
        guard let editing else { return }
        do {
            let dup = try service.duplicateProfile(id: editing.id)
            reloadAll()
            select(dup, persistSelection: true)
            setStatus("Duplicated as \(dup.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func deleteSelected() {
        guard let editing else { return }
        guard profiles.count > 1 else {
            showError("The last remaining profile can't be deleted — every window needs one to bind to.")
            return
        }
        let alert = NSAlert()
        alert.messageText = "Delete profile “\(editing.name)”?"
        alert.informativeText = "This removes the profile container only — no cloud account credentials are touched. Any window already open for it is not automatically closed."
        alert.addButton(withTitle: "Delete Profile")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do {
            try service.deleteProfile(id: editing.id)
            reloadAll()
            setStatus("Deleted \(editing.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func exportSelected() {
        guard let editing else { return }
        let panel = NSSavePanel()
        panel.title = "Export \(editing.name)"
        panel.nameFieldStringValue = "\(editing.name).ezprofile"
        panel.canCreateDirectories = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            try service.exportProfile(id: editing.id, to: url)
            setStatus("Exported \(editing.name) → \(url.lastPathComponent)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func importFromFile() {
        let panel = NSOpenPanel()
        panel.title = "Import a .ezprofile file"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            let imported = try service.importProfile(from: url)
            reloadAll()
            select(imported, persistSelection: true)
            setStatus("Imported as \(imported.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func openWindowForSelected() {
        guard let editing else { return }
        NotificationCenter.default.post(name: .openProfileWindowRequested, object: editing.id)
    }

    // MARK: - Detail editing (whole-object PUT on Save)

    @objc func saveDetail() {
        guard var profile = editing else { return }
        let newName = nameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !newName.isEmpty else {
            showError("Profile name is required.")
            return
        }
        profile.name = newName
        profile.showAllAccounts = showAllCheckbox.state == .on
        do {
            let saved = try service.saveProfile(profile)
            reloadAll()
            select(saved, persistSelection: true)
            // Any open ProfileWindowController for this id re-fetches and
            // re-renders rather than going stale.
            NotificationCenter.default.post(name: .profileDidChange, object: saved.id)
            setStatus("Saved \(saved.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func toggleAccount(_ sender: NSButton) {
        guard var profile = editing, sender.tag >= 0, sender.tag < allAccounts.count else { return }
        let account = allAccounts[sender.tag]
        if let idx = profile.accounts.firstIndex(of: account) {
            profile.accounts.remove(at: idx)
        } else {
            profile.accounts.append(account)
        }
        editing = profile
        updateSaveButton()
    }

    @objc func addEnvVar() {
        guard var profile = editing else { return }
        profile.envVars.append(EnvVar(key: "", value: ""))
        editing = profile
        envVarsTable.reloadData()
        updateSaveButton()
    }

    @objc func removeEnvVar() {
        guard var profile = editing else { return }
        let row = envVarsTable.selectedRow
        guard row >= 0, row < profile.envVars.count else { return }
        profile.envVars.remove(at: row)
        editing = profile
        envVarsTable.reloadData()
        updateSaveButton()
    }

    @objc func envKeyEdited(_ sender: NSTextField) {
        guard var profile = editing, sender.tag >= 0, sender.tag < profile.envVars.count else { return }
        profile.envVars[sender.tag].key = sender.stringValue
        editing = profile
        updateSaveButton()
    }

    @objc func envValueEdited(_ sender: NSTextField) {
        guard var profile = editing, sender.tag >= 0, sender.tag < profile.envVars.count else { return }
        profile.envVars[sender.tag].value = sender.stringValue
        editing = profile
        updateSaveButton()
    }

    func controlTextDidChange(_ obj: Notification) {
        guard let field = obj.object as? NSTextField, field == nameField else { return }
        updateSaveButton()
    }

    // MARK: - Table plumbing

    func numberOfRows(in tableView: NSTableView) -> Int {
        if tableView == profilesTable { return profiles.count }
        if tableView == accountsTable { return allAccounts.count }
        if tableView == envVarsTable { return editing?.envVars.count ?? 0 }
        return 0
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if tableView == profilesTable { return profileRow(tableView, row: row) }
        if tableView == accountsTable { return accountRow(tableView, row: row) }
        if tableView == envVarsTable { return envVarRow(tableView, tableColumn: tableColumn, row: row) }
        return nil
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard notification.object as? NSTableView == profilesTable else { return }
        let row = profilesTable.selectedRow
        guard row >= 0, row < profiles.count else { return }
        select(profiles[row])
    }

    private func profileRow(_ table: NSTableView, row: Int) -> NSView {
        let id = NSUserInterfaceItemIdentifier("pmProfileCell")
        let cell = (table.makeView(withIdentifier: id, owner: self) as? NSTableCellView) ?? makeLabelCell(id)
        let p = profiles[row]
        cell.textField?.stringValue = p.showAllAccounts ? "\(p.name)  (all accounts)" : "\(p.name)  (\(p.accounts.count))"
        return cell
    }

    private func accountRow(_ table: NSTableView, row: Int) -> NSView {
        let id = NSUserInterfaceItemIdentifier("pmAccountCell")
        let cell = (table.makeView(withIdentifier: id, owner: self) as? AccountCheckboxCellView)
            ?? AccountCheckboxCellView(reuseIdentifier: id, target: self, action: #selector(toggleAccount(_:)))
        let account = allAccounts[row]
        let isMember = editing?.accounts.contains(account) ?? false
        cell.configure(row: row, title: "\(account.provider) · \(account.account)", checked: isMember)
        return cell
    }

    private func envVarRow(_ table: NSTableView, tableColumn: NSTableColumn?, row: Int) -> NSView {
        guard let editing, row < editing.envVars.count else { return NSView() }
        let isKeyColumn = tableColumn?.identifier.rawValue == "key"
        let id = NSUserInterfaceItemIdentifier(isKeyColumn ? "pmEnvKey" : "pmEnvValue")
        let field = (table.makeView(withIdentifier: id, owner: self) as? NSTextField) ?? {
            let f = NSTextField(string: "")
            f.identifier = id
            f.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
            f.isBordered = false
            f.drawsBackground = false
            f.focusRingType = .none
            f.target = self
            f.action = isKeyColumn ? #selector(envKeyEdited(_:)) : #selector(envValueEdited(_:))
            return f
        }()
        field.tag = row
        field.stringValue = isKeyColumn ? editing.envVars[row].key : editing.envVars[row].value
        field.placeholderString = isKeyColumn ? "KEY" : "value"
        return field
    }

    private func makeLabelCell(_ id: NSUserInterfaceItemIdentifier) -> NSTableCellView {
        let cell = NSTableCellView()
        cell.identifier = id
        let text = NSTextField(labelWithString: "")
        text.font = .systemFont(ofSize: 13)
        text.lineBreakMode = .byTruncatingTail
        text.translatesAutoresizingMaskIntoConstraints = false
        cell.addSubview(text)
        cell.textField = text
        NSLayoutConstraint.activate([
            text.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 8),
            text.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -6),
            text.centerYAnchor.constraint(equalTo: cell.centerYAnchor)
        ])
        return cell
    }
}

extension Notification.Name {
    /// Posted by ProfileManagerWindowController; object: the profile id
    /// (String) whose window AppDelegate should open (or refocus).
    static let openProfileWindowRequested = Notification.Name("EZCloudManager.openProfileWindowRequested")
}

/// One Accounts-membership row: a checkbox + "provider · account" label.
final class AccountCheckboxCellView: NSTableCellView {
    private let checkbox = NSButton(checkboxWithTitle: "", target: nil, action: nil)

    init(reuseIdentifier: NSUserInterfaceItemIdentifier, target: AnyObject, action: Selector) {
        super.init(frame: .zero)
        identifier = reuseIdentifier
        checkbox.target = target
        checkbox.action = action
        checkbox.translatesAutoresizingMaskIntoConstraints = false
        addSubview(checkbox)
        NSLayoutConstraint.activate([
            checkbox.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 6),
            checkbox.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -6),
            checkbox.centerYAnchor.constraint(equalTo: centerYAnchor)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(row: Int, title: String, checked: Bool) {
        checkbox.tag = row
        checkbox.title = title
        checkbox.state = checked ? .on : .off
    }
}
