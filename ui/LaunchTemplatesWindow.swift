import AppKit

/// EC2 Launch Templates, edited like a plain config file.
///
/// AWS launch templates are immutable per version — the console "edit" story
/// is create-a-new-version-by-hand. This window hides that ceremony behind
/// the safe best practice: **clone → edit → apply as new version → set
/// default**, with an explicit diff confirmation before anything is written,
/// one-click rollback (set default back), and version deletion only as a
/// separate, explicit, confirmed action. The source version is never
/// mutated, so every apply has an undo point by construction.
final class LaunchTemplatesWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate {
    private let service: CredentialsService

    var profile = ""
    var regionField: NSTextField!
    var templatesTable: NSTableView!
    var fieldsTable: NSTableView!
    var versionPopup: NSPopUpButton!
    var fieldSearch: NSSearchField!
    var descriptionField: NSTextField!
    var setDefaultCheckbox: NSButton!
    var applyButton: NSButton!
    var rollbackButton: NSButton!
    var deleteVersionButton: NSButton!
    var statusLabel: NSTextField!
    var spinner: NSProgressIndicator!

    var templates: [LaunchTemplate] = []
    var versions: [LaunchTemplateVersion] = []
    var currentTemplate: LaunchTemplate?
    var loadedVersion: String = ""
    /// Flattened LaunchTemplateData as loaded — the diff baseline.
    var originalFlat: [String: String] = [:]
    /// Working copy with edits (keys added by the user included).
    var editedFlat: [String: String] = [:]
    /// Row order for the fields table after search filtering.
    var visibleKeys: [String] = []
    /// Default version before the last apply — the rollback target.
    var rollbackVersion: Int64?

    init(service: CredentialsService) {
        self.service = service
        super.init(window: nil)
        buildWindow()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func present(profile: String, region: String) {
        self.profile = profile
        regionField.stringValue = region
        window?.title = "EC2 Launch Templates — \(profile)"
        window?.makeKeyAndOrderFront(nil)
        loadTemplates()
    }

    // MARK: - Async plumbing

    private func busy(_ message: String) {
        statusLabel.stringValue = message
        spinner.startAnimation(nil)
    }

    private func done(_ message: String) {
        statusLabel.stringValue = message
        spinner.stopAnimation(nil)
    }

    private func failed(_ error: Error) {
        spinner.stopAnimation(nil)
        statusLabel.stringValue = "Error"
        let alert = NSAlert()
        alert.messageText = "Launch Templates"
        alert.informativeText = error.localizedDescription
        alert.alertStyle = .warning
        alert.runModal()
    }

    private func region() -> String {
        regionField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: - Loading

    @objc func loadTemplates() {
        guard !region().isEmpty else { return }
        let (p, r) = (profile, region())
        busy("Loading launch templates from \(r)…")
        service.runAsync({ try self.service.launchTemplates(profile: p, region: r) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let templates):
                self.templates = templates.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
                self.templatesTable.reloadData()
                self.clearEditor()
                self.done("\(templates.count) template(s) in \(r)")
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    private func loadVersions(for template: LaunchTemplate, thenSelect version: String? = nil) {
        let (p, r) = (profile, region())
        busy("Loading versions of \(template.name)…")
        service.runAsync({ try self.service.launchTemplateVersions(profile: p, region: r, name: template.name) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let versions):
                self.versions = versions.sorted { $0.number > $1.number }
                self.rebuildVersionPopup()
                let target = version ?? String(versions.first { $0.isDefault }?.number ?? template.defaultVersion)
                self.selectVersion(target)
                self.loadVersionData(template: template, version: target)
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    private func loadVersionData(template: LaunchTemplate, version: String) {
        let (p, r) = (profile, region())
        busy("Loading \(template.name) v\(version)…")
        service.runAsync({ try self.service.launchTemplateData(profile: p, region: r, name: template.name, version: version) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let data):
                self.originalFlat = data.fields
                self.editedFlat = data.fields
                self.loadedVersion = version
                self.applyFieldFilter()
                self.updateApplyState()
                self.done("\(template.name) v\(version) — \(data.fields.count) fields (source version stays untouched)")
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    private func clearEditor() {
        currentTemplate = nil
        versions = []
        originalFlat = [:]
        editedFlat = [:]
        visibleKeys = []
        loadedVersion = ""
        versionPopup.removeAllItems()
        fieldsTable.reloadData()
        updateApplyState()
    }

    private func rebuildVersionPopup() {
        versionPopup.removeAllItems()
        for v in versions {
            let flags = [v.isDefault ? "default" : nil, v.description?.isEmpty == false ? v.description : nil]
                .compactMap { $0 }.joined(separator: " · ")
            versionPopup.addItem(withTitle: flags.isEmpty ? "v\(v.number)" : "v\(v.number) — \(flags)")
            versionPopup.lastItem?.representedObject = String(v.number)
        }
    }

    private func selectVersion(_ version: String) {
        for (idx, item) in (versionPopup.itemArray).enumerated() where (item.representedObject as? String) == version {
            versionPopup.selectItem(at: idx)
            return
        }
    }

    // MARK: - Editing model

    private func applyFieldFilter() {
        let query = fieldSearch.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        var keys = Set(originalFlat.keys).union(editedFlat.keys).sorted()
        if !query.isEmpty {
            keys = keys.filter { $0.lowercased().contains(query) || (editedFlat[$0] ?? "").lowercased().contains(query) }
        }
        visibleKeys = keys
        fieldsTable.reloadData()
    }

    private func changedKeys() -> [String] {
        var keys: [String] = []
        for key in Set(originalFlat.keys).union(editedFlat.keys).sorted() {
            if (originalFlat[key] ?? "") != (editedFlat[key] ?? "") {
                keys.append(key)
            }
        }
        return keys
    }

    private func updateApplyState() {
        let changed = changedKeys()
        applyButton.isEnabled = currentTemplate != nil && !changed.isEmpty
        applyButton.title = changed.isEmpty ? "Apply as New Version" : "Apply as New Version (\(changed.count))"
        rollbackButton.isHidden = rollbackVersion == nil
        deleteVersionButton.isEnabled = currentTemplate != nil && versions.count > 1
    }

    // MARK: - Apply / rollback / delete

    @objc func applyEdits() {
        guard let template = currentTemplate else { return }
        let changed = changedKeys()
        guard !changed.isEmpty else { return }

        // Only send actual edits: flatjson treats an empty value on an
        // existing path as "remove that key".
        var edits: [String: String] = [:]
        for key in changed {
            edits[key] = editedFlat[key] ?? ""
        }

        let alert = NSAlert()
        alert.messageText = "Create new version of “\(template.name)”?"
        alert.informativeText = "A new version is created from v\(loadedVersion) with \(changed.count) change\(changed.count == 1 ? "" : "s")\(setDefaultCheckbox.state == .on ? " and becomes the default version" : ""). v\(loadedVersion) is not modified — you can roll back at any time. Nothing is deleted."
        alert.accessoryView = diffView(changed: changed)
        alert.addButton(withTitle: "Create Version")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        let (p, r) = (profile, region())
        let source = loadedVersion
        let description = descriptionField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let setDefault = setDefaultCheckbox.state == .on
        let previousDefault = versions.first { $0.isDefault }?.number
        busy("Creating new version of \(template.name)…")
        service.runAsync({
            try self.service.applyLaunchTemplate(
                profile: p, region: r, name: template.name,
                sourceVersion: source,
                description: description.isEmpty ? "Edited with EZ Cloud Manager (from v\(source))" : description,
                setDefault: setDefault, fields: edits)
        }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let response):
                if setDefault { self.rollbackVersion = previousDefault }
                self.descriptionField.stringValue = ""
                self.loadVersions(for: template, thenSelect: String(response.newVersion))
                self.done("Created v\(response.newVersion)\(setDefault ? " and set as default" : "") — rollback available")
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    @objc func rollback() {
        guard let template = currentTemplate, let target = rollbackVersion else { return }
        let alert = NSAlert()
        alert.messageText = "Roll back default version?"
        alert.informativeText = "The default version of “\(template.name)” goes back to v\(target). No versions are deleted."
        alert.addButton(withTitle: "Roll Back")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        let (p, r) = (profile, region())
        busy("Rolling back default to v\(target)…")
        service.runAsync({ try self.service.setLaunchTemplateDefault(profile: p, region: r, name: template.name, version: String(target)) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success:
                self.rollbackVersion = nil
                self.loadVersions(for: template, thenSelect: String(target))
                self.done("Default rolled back to v\(target)")
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    @objc func deleteSelectedVersion() {
        guard let template = currentTemplate,
              let version = versionPopup.selectedItem?.representedObject as? String else { return }
        if versions.first(where: { String($0.number) == version })?.isDefault == true {
            failed(CredentialsService.ServiceError.toolFailed("v\(version) is the default version — make another version default first."))
            return
        }
        let alert = NSAlert()
        alert.alertStyle = .critical
        alert.messageText = "Permanently delete v\(version) of “\(template.name)”?"
        alert.informativeText = "Deleting a launch template version cannot be undone. Instances already launched from it are not affected."
        alert.addButton(withTitle: "Delete Version")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        let (p, r) = (profile, region())
        busy("Deleting v\(version)…")
        service.runAsync({ try self.service.deleteLaunchTemplateVersions(profile: p, region: r, name: template.name, versions: [version]) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success:
                self.loadVersions(for: template)
                self.done("Deleted v\(version)")
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    /// Compact monospaced old → new listing for the apply confirmation.
    private func diffView(changed: [String]) -> NSView {
        let text = NSMutableAttributedString()
        let mono = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
        for key in changed {
            let old = originalFlat[key] ?? "∅"
            let new = editedFlat[key]?.isEmpty == false ? editedFlat[key]! : "∅ (removed)"
            text.append(NSAttributedString(string: "\(key)\n", attributes: [.font: mono, .foregroundColor: NSColor.labelColor]))
            text.append(NSAttributedString(string: "    \(old)  →  \(new)\n", attributes: [.font: mono, .foregroundColor: NSColor.secondaryLabelColor]))
        }
        let view = NSTextView()
        view.isEditable = false
        view.textContainerInset = NSSize(width: 10, height: 10)
        view.textStorage?.setAttributedString(text)
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .lineBorder
        scroll.documentView = view
        scroll.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            scroll.widthAnchor.constraint(equalToConstant: 460),
            scroll.heightAnchor.constraint(equalToConstant: min(300, CGFloat(changed.count) * 34 + 24))
        ])
        return scroll
    }

    // MARK: - Table plumbing

    func numberOfRows(in tableView: NSTableView) -> Int {
        tableView == templatesTable ? templates.count : visibleKeys.count
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        if tableView == templatesTable {
            guard row < templates.count else { return nil }
            let template = templates[row]
            let id = NSUserInterfaceItemIdentifier("ltCell")
            let cell = (tableView.makeView(withIdentifier: id, owner: self) as? NSTableCellView) ?? {
                let c = NSTableCellView(); c.identifier = id
                let t = NSTextField(labelWithString: ""); t.font = .systemFont(ofSize: 12)
                t.lineBreakMode = .byTruncatingTail
                t.translatesAutoresizingMaskIntoConstraints = false
                c.addSubview(t); c.textField = t
                NSLayoutConstraint.activate([
                    t.leadingAnchor.constraint(equalTo: c.leadingAnchor, constant: 8),
                    t.trailingAnchor.constraint(equalTo: c.trailingAnchor, constant: -6),
                    t.centerYAnchor.constraint(equalTo: c.centerYAnchor)])
                return c
            }()
            cell.textField?.stringValue = template.name
            cell.toolTip = "\(template.id) · default v\(template.defaultVersion) · latest v\(template.latestVersion)"
            return cell
        }

        guard row < visibleKeys.count else { return nil }
        let key = visibleKeys[row]
        let isKeyColumn = tableColumn?.identifier.rawValue == "key"
        let id = NSUserInterfaceItemIdentifier(isKeyColumn ? "ltKey" : "ltValue")
        let field = (tableView.makeView(withIdentifier: id, owner: self) as? NSTextField) ?? {
            let f = NSTextField(string: "")
            f.identifier = id
            f.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
            f.isBordered = false
            f.drawsBackground = false
            f.focusRingType = .none
            f.usesSingleLineMode = true
            f.lineBreakMode = .byTruncatingTail
            f.cell?.isScrollable = true
            if !isKeyColumn {
                f.target = self
                f.action = #selector(self.valueEdited(_:))
            }
            return f
        }()
        if isKeyColumn {
            field.isEditable = false
            field.textColor = originalFlat[key] == nil ? .systemBlue : .secondaryLabelColor
            field.stringValue = key
        } else {
            field.isEditable = true
            let value = editedFlat[key] ?? ""
            field.textColor = (originalFlat[key] ?? "") != value ? .systemOrange : .labelColor
            field.stringValue = value
            field.tag = row
        }
        return field
    }

    func tableViewSelectionDidChange(_ notification: Notification) {
        guard notification.object as? NSTableView == templatesTable else { return }
        let row = templatesTable.selectedRow
        guard row >= 0, row < templates.count else { return }
        rollbackVersion = nil
        currentTemplate = templates[row]
        loadVersions(for: templates[row])
    }

    @objc func valueEdited(_ sender: NSTextField) {
        let row = sender.tag
        guard row >= 0, row < visibleKeys.count else { return }
        editedFlat[visibleKeys[row]] = sender.stringValue
        updateApplyState()
        fieldsTable.reloadData(forRowIndexes: IndexSet(integer: row), columnIndexes: IndexSet(integersIn: 0..<2))
    }

    @objc func versionPicked(_ sender: NSPopUpButton) {
        guard let template = currentTemplate,
              let version = sender.selectedItem?.representedObject as? String else { return }
        loadVersionData(template: template, version: version)
    }

    @objc func addField() {
        let alert = NSAlert()
        alert.messageText = "Add field"
        alert.informativeText = "Dotted path into LaunchTemplateData, e.g. InstanceType or TagSpecifications[0].Tags[0].Value"
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 300, height: 24))
        field.placeholderString = "InstanceType"
        alert.accessoryView = field
        alert.addButton(withTitle: "Add")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let key = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !key.isEmpty else { return }
        editedFlat[key] = editedFlat[key] ?? ""
        applyFieldFilter()
        updateApplyState()
    }

    func controlTextDidChange(_ obj: Notification) {
        if obj.object as? NSSearchField == fieldSearch {
            applyFieldFilter()
        }
    }
}
