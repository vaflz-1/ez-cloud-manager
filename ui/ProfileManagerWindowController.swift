import AppKit

private struct ProfileCoreDraft {
    let name: String
    let envVars: [EnvVar]
    /// The server core snapshot this draft was originally based on. Ordinary
    /// profile-list reloads must preserve it so they cannot silently turn a
    /// stale draft into an overwrite that passes compare-and-swap.
    let expectedName: String
    let expectedEnvVars: [EnvVar]
}

/// App-global, profile-list utility window — NOT bound to any single
/// profile (contrast ProfileWindowController). Provides profile CRUD
/// (create/rename-via-Save/duplicate/delete/export/import), an env var
/// editor, a read-only enabled-plugins summary, and "Open Window" for any
/// profile.
///
/// P1.5 (docs/PLATFORM.md principle 5, "core owns no plugin data"): this
/// window no longer has an Accounts membership editor at all — account
/// scoping is the Cloud Accounts plugin's own settings, edited from that
/// plugin's own "Scope" toolbar button
/// (ui/CloudAccountsWindowController+Scope.swift). Export/Import/Duplicate
/// DO still live here (a design reversal from an earlier draft of this
/// window that removed them entirely) — re-homed onto the sidebar's "⋯"
/// pull-down menu instead of always-visible buttons, which is what actually
/// makes the button row structurally impossible to overflow (two segments +
/// one menu button, not five buttons), not the removal itself.
final class ProfileManagerWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate {
    let service: CredentialsService
    private var profileChangeObserver: NSObjectProtocol?
    private var profileListChangeObserver: NSObjectProtocol?

    var profilesTable: NSTableView!
    var envVarsTable: NSTableView!
    var nameField: NSTextField!
    var statusLabel: NSTextField!
    var saveButton: NSButton!
    /// Read-only PLUGINS card chips (see reloadPluginsCard()).
    var pluginsStack: NSStackView!
    var pluginsEmptyLabel: NSTextField!

    /// All known profiles, sorted by name — the left-hand list.
    var profiles: [Profile] = []
    /// The profile currently shown in the detail pane (a working copy;
    /// edits stay local until Save patches the core fields this window owns).
    var editing: Profile?
    /// Core fields from the last server snapshot. Save is enabled only when
    /// the editor differs from this baseline; addon-owned fields are never
    /// part of the dirty comparison or the save request.
    private var savedName = ""
    private var savedEnvVars: [EnvVar] = []
    /// Unsaved core drafts survive switching between rows without autosave or
    /// a discard prompt. They remain in-memory until explicitly saved.
    private var coreDrafts: [String: ProfileCoreDraft] = [:]
    /// The selected profile's enabled plugins, for the read-only PLUGINS
    /// card — sourced from `ezcloud plugins list --profile ID` (already
    /// filters/annotates by this profile), never editable here (enabling or
    /// disabling a plugin is exclusively the Hub's Add Plugins catalog).
    var enabledPluginDescriptors: [PluginDescriptor] = []

    init(service: CredentialsService) {
        self.service = service
        super.init(window: nil)
        buildWindow()
        profileChangeObserver = NotificationCenter.default.addObserver(
            forName: .profileDidChange,
            object: nil,
            queue: .main
        ) { [weak self] note in
            self?.refreshAddonState(after: note)
        }
        profileListChangeObserver = NotificationCenter.default.addObserver(
            forName: .profileListDidChange,
            object: nil,
            queue: .main
        ) { [weak self] note in
            self?.refreshProfileList(after: note)
        }
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    deinit {
        if let profileChangeObserver {
            NotificationCenter.default.removeObserver(profileChangeObserver)
        }
        if let profileListChangeObserver {
            NotificationCenter.default.removeObserver(profileListChangeObserver)
        }
    }

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

    private func select(_ profile: Profile, persistSelection: Bool = false) {
        captureCurrentDraft()
        var selected = profile
        if let draft = coreDrafts[profile.id] {
            selected.name = draft.name
            selected.envVars = draft.envVars
            savedName = draft.expectedName
            savedEnvVars = draft.expectedEnvVars
        } else {
            savedName = profile.name
            savedEnvVars = profile.envVars
        }
        editing = selected
        nameField.stringValue = selected.name
        envVarsTable.reloadData()
        enabledPluginDescriptors = (try? service.listPlugins(profileID: profile.id).filter { $0.enabled }) ?? []
        reloadPluginsCard()
        updateSaveButton()
        if persistSelection, let idx = profiles.firstIndex(where: { $0.id == profile.id }) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        }
    }

    private func captureCurrentDraft() {
        guard let editing else { return }
        let name = nameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if name != savedName || editing.envVars != savedEnvVars {
            let existing = coreDrafts[editing.id]
            coreDrafts[editing.id] = ProfileCoreDraft(
                name: name,
                envVars: editing.envVars,
                expectedName: existing?.expectedName ?? savedName,
                expectedEnvVars: existing?.expectedEnvVars ?? savedEnvVars
            )
        } else {
            coreDrafts.removeValue(forKey: editing.id)
        }
    }

    private func clearDetail() {
        savedName = ""
        savedEnvVars = []
        nameField.stringValue = ""
        envVarsTable.reloadData()
        enabledPluginDescriptors = []
        reloadPluginsCard()
        updateSaveButton()
    }

    private func setStatus(_ message: String) {
        statusLabel.stringValue = message
    }

    /// Refreshes fields owned by addons without replacing an unsaved core
    /// draft. This keeps the plugin summary/timestamp live while targeted
    /// Profile Manager edits remain intact.
    private func refreshAddonState(after note: Notification) {
        guard let changedID = note.object as? String,
              var draft = editing,
              draft.id == changedID,
              let fresh = try? service.getProfile(id: changedID)
        else { return }

        draft.enabledPlugins = fresh.enabledPlugins
        draft.settings = fresh.settings
        draft.windowState = fresh.windowState
        draft.version = fresh.version
        draft.savedAt = fresh.savedAt
        draft.updatedAt = fresh.updatedAt
        editing = draft

        if let index = profiles.firstIndex(where: { $0.id == changedID }) {
            profiles[index] = fresh
        }
        enabledPluginDescriptors = (try? service.listPlugins(profileID: changedID).filter { $0.enabled }) ?? []
        reloadPluginsCard()
        updateSaveButton()
    }

    /// Keeps this app-global list current when a mutation originates in any
    /// window (for example the Hub switcher's New Profile action or Transfer
    /// import). `reloadAll` captures and reapplies the selected profile's
    /// unsaved core draft. Only a draft for a profile that was actually
    /// deleted is discarded.
    private func refreshProfileList(after note: Notification) {
        guard let change = note.object as? ProfileListChange else { return }
        if change.mutation == .deleted {
            coreDrafts.removeValue(forKey: change.profileID)
            if editing?.id == change.profileID {
                editing = nil
            }
        }
        reloadAll()
    }

    /// Resolves an optimistic core conflict without discarding or immediately
    /// overwriting either side. The user's current fields remain visible as a
    /// draft, while the latest server core becomes the new explicit expected
    /// baseline. A second Save is therefore an intentional action after the
    /// user has reviewed the preserved draft.
    private func recoverFromCoreConflict(draft: Profile) {
        // Preserve the draft before the reload attempt too: if fetching the
        // latest snapshot fails, switching rows still cannot lose the edit.
        coreDrafts[draft.id] = ProfileCoreDraft(
            name: draft.name,
            envVars: draft.envVars,
            expectedName: savedName,
            expectedEnvVars: savedEnvVars
        )

        do {
            let fresh = try service.getProfile(id: draft.id)
            coreDrafts[draft.id] = ProfileCoreDraft(
                name: draft.name,
                envVars: draft.envVars,
                expectedName: fresh.name,
                expectedEnvVars: fresh.envVars
            )

            if let index = profiles.firstIndex(where: { $0.id == draft.id }) {
                profiles[index] = fresh
            }

            var preserved = fresh
            preserved.name = draft.name
            preserved.envVars = draft.envVars
            editing = preserved
            savedName = fresh.name
            savedEnvVars = fresh.envVars
            nameField.stringValue = draft.name
            envVarsTable.reloadData()
            enabledPluginDescriptors = (try? service.listPlugins(profileID: draft.id).filter { $0.enabled }) ?? []
            reloadPluginsCard()
            updateSaveButton()
            setStatus("Draft preserved — review and Save again")
        } catch {
            updateSaveButton()
            setStatus("Draft preserved — latest profile could not be reloaded")
            showError("The profile changed elsewhere. Your draft is preserved, but the latest version could not be loaded: \(error.localizedDescription)")
        }
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "Manage Profiles"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }

    private func updateSaveButton() {
        guard let editing else {
            saveButton.isEnabled = false
            window?.isDocumentEdited = false
            setStatus("Ready")
            return
        }
        let name = nameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let dirty = name != savedName || editing.envVars != savedEnvVars
        saveButton.isEnabled = dirty
        window?.isDocumentEdited = dirty
        setStatus(dirty ? "Unsaved changes" : "Last saved \(AppTimestamp.display(editing.savedAt))")
    }

    // MARK: - Sidebar controls (NSSegmentedControl actions, never manual frames)

    @objc func listSegmentChanged(_ sender: NSSegmentedControl) {
        sender.selectedSegment == 0 ? newProfile() : deleteSelected()
    }

    @objc func envSegmentChanged(_ sender: NSSegmentedControl) {
        sender.selectedSegment == 0 ? addEnvVar() : removeEnvVar()
    }

    // MARK: - PLUGINS card (read-only — enable/disable only via the Hub's catalog)

    func reloadPluginsCard() {
        pluginsStack.arrangedSubviews.forEach { $0.removeFromSuperview() }
        pluginsEmptyLabel.isHidden = !enabledPluginDescriptors.isEmpty
        for p in enabledPluginDescriptors {
            pluginsStack.addArrangedSubview(pluginChip(p))
        }
    }

    private func pluginChip(_ p: PluginDescriptor) -> NSView {
        let icon = NSImageView()
        let cfg = NSImage.SymbolConfiguration(pointSize: 11, weight: .medium)
        icon.image = NSImage(systemSymbolName: p.icon, accessibilityDescription: p.name)?.withSymbolConfiguration(cfg)
        icon.contentTintColor = PluginStyle.color(p.category)
        let label = NSTextField(labelWithString: p.name)
        label.font = .systemFont(ofSize: 11)
        let chip = NSStackView(views: [icon, label])
        chip.orientation = .horizontal
        chip.spacing = 4
        return chip
    }

    // MARK: - Profile list actions

    @objc func newProfile() {
        guard let name = ProfileCreationPrompt.run() else { return }
        do {
            let created = try service.createProfile(name: name)
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
        alert.informativeText = "This removes the profile container and closes its window and addon windows. No cloud account credentials are touched."
        alert.addButton(withTitle: "Delete Profile")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do {
            try service.deleteProfile(id: editing.id)
            setStatus("Deleted \(editing.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func openWindowForSelected() {
        guard let editing else { return }
        NotificationCenter.default.post(name: .openProfileWindowRequested, object: editing.id)
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
            select(imported, persistSelection: true)
            setStatus("Imported as \(imported.name)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    // MARK: - Detail editing (targeted core-field Save)

    @objc func saveDetail() {
        guard var profile = editing else { return }
        let newName = nameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !newName.isEmpty else {
            showError("Profile name is required.")
            return
        }
        profile.name = newName
        do {
            let saved = try service.saveProfile(
                profile,
                expectedName: savedName,
                expectedEnvVars: savedEnvVars
            )
            coreDrafts.removeValue(forKey: saved.id)
            editing = saved
            savedName = saved.name
            savedEnvVars = saved.envVars
            nameField.stringValue = saved.name
            reloadAll()
            // Any open ProfileWindowController for this id re-fetches and
            // re-renders rather than going stale.
            NotificationCenter.default.post(name: .profileDidChange, object: saved.id)
            setStatus("Saved \(saved.name) · \(AppTimestamp.display(saved.savedAt))")
        } catch CredentialsService.ServiceError.profileCoreConflict {
            recoverFromCoreConflict(draft: profile)
        } catch {
            showError(error.localizedDescription)
        }
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
        guard let field = obj.object as? NSTextField else { return }
        if field == nameField {
            updateSaveButton()
            return
        }

        guard var profile = editing,
              field.tag >= 0,
              field.tag < profile.envVars.count
        else { return }
        switch field.identifier?.rawValue {
        case "pmEnvKey":
            profile.envVars[field.tag].key = field.stringValue
        case "pmEnvValue":
            profile.envVars[field.tag].value = field.stringValue
        default:
            return
        }
        editing = profile
        updateSaveButton()
    }

    // MARK: - Table plumbing

    func numberOfRows(in tableView: NSTableView) -> Int {
        if tableView == profilesTable { return profiles.count }
        if tableView == envVarsTable { return editing?.envVars.count ?? 0 }
        return 0
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if tableView == profilesTable { return profileRow(tableView, row: row) }
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
        cell.textField?.stringValue = p.name
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
        field.delegate = self
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

/// The shared "New profile" prompt — used by both this window's New… button
/// and the Hub's profile switcher's "New Profile…" row
/// (ui/ProfileWindowController+ProfileBar.swift), so the alert text and
/// field only exist once.
enum ProfileCreationPrompt {
    /// Shows the alert; returns the trimmed name if the user confirmed with
    /// non-empty text, nil otherwise (cancelled or left blank).
    static func run() -> String? {
        let alert = NSAlert()
        alert.messageText = "New profile"
        alert.informativeText = "A profile is a global container: cloud account references, env vars, and (later) plugins/settings. Each app window binds to exactly one."
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        field.placeholderString = "e.g. acme-prod"
        alert.accessoryView = field
        alert.addButton(withTitle: "Create")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return nil }
        let name = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty ? nil : name
    }
}
