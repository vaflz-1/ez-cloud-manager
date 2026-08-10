import AppKit

/// EC2 Launch Templates, edited like a plain config file — the
/// **ec2-launch-templates** built-in plugin (internal/plugin.LaunchTemplatesID).
///
/// AWS launch templates are immutable per version — the console "edit" story
/// is create-a-new-version-by-hand. This window hides that ceremony behind
/// the safe best practice: **clone → edit → apply as new version → set
/// default**, with an explicit diff confirmation before anything is written,
/// one-click rollback (set default back), and version deletion only as a
/// separate, explicit, confirmed action. The source version is never
/// mutated, so every apply has an undo point by construction.
///
/// P1 (docs/PLATFORM.md): this window used to get its AWS profile name +
/// region from ProfileWindowController's embedded sidebar selection. Now
/// that the Hub has no sidebar, it owns a small AWS-profile picker of its
/// own and reads AWS profiles directly from the provider rail. The owning
/// global profile contributes only its non-secret environment.
final class LaunchTemplatesWindowController: NSWindowController, NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate {
    private let service: CredentialsService

    /// The global profile this Launch Templates window is scoped to. It can
    /// be refreshed while the window is open when core fields change.
    private(set) var owningProfile: Profile?
    /// The selected AWS credential-entry name (existing, unrelated meaning —
    /// see CloudAccountsWindowController's own terminology note).
    var profile = ""
    var awsProfilePopup: NSPopUpButton!
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

    /// Presents the window scoped to `profile` — reloads its AWS-profile
    /// picker (and, through it, the template list) fresh every time, so a
    /// reopened window is never stale.
    func present(owningProfile profile: Profile) {
        owningProfile = profile
        window?.title = "Launch Templates — \(profile.name)"
        window?.makeKeyAndOrderFront(nil)
        loadAwsProfiles()
    }

    /// Refreshes metadata after a same-profile mutation whose environment did
    /// not change. The Hub destroys this controller instead when envVars
    /// change, so a draft can never cross cloud contexts.
    func updateOwningProfile(_ profile: Profile) {
        owningProfile = profile
        guard window?.isVisible == true else { return }
        let selected = self.profile.isEmpty ? "" : " · \(self.profile)"
        window?.title = "Launch Templates — \(profile.name)\(selected)"
    }

    /// Populates the AWS-profile picker directly from the provider rail,
    /// preserving the previously chosen profile across a reopen when it is
    /// still available. Launch Templates deliberately does not read another
    /// plugin's settings, so it remains usable when Cloud Accounts is disabled.
    private func loadAwsProfiles() {
        guard let owningProfile else { return }
        busy("Loading AWS profiles…")
        service.runAsync({ try self.service.list(provider: "aws", extraEnv: owningProfile.envVars.asDictionary()) }) { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let response):
                let names = response.profiles.map(\.name)
                guard !names.isEmpty else {
                    self.done("No AWS CLI profiles are available in “\(owningProfile.name)”. Configure one first, then reopen Launch Templates.")
                    return
                }
                let previous = self.profile
                self.awsProfilePopup.removeAllItems()
                self.awsProfilePopup.addItems(withTitles: names)
                if let idx = names.firstIndex(of: previous) {
                    self.awsProfilePopup.selectItem(at: idx)
                }
                self.awsProfilePopupChanged(self.awsProfilePopup)
            case .failure(let error):
                self.failed(error)
            }
        }
    }

    @objc func awsProfilePopupChanged(_ sender: NSPopUpButton) {
        guard let selected = sender.titleOfSelectedItem else { return }
        profile = selected
        window?.title = "Launch Templates — \(owningProfile?.name ?? "") · \(selected)"
        prefillRegionIfNeeded()
        loadTemplates()
    }

    /// Fills the region field from the chosen AWS profile's own "region"
    /// field the first time it's picked; leaves a user-edited value alone.
    private func prefillRegionIfNeeded() {
        guard regionField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        guard owningProfile != nil else { return }
        let region = (try? service.get(provider: "aws", profile, extraEnv: extraEnv()).fields["region"])
            .flatMap { $0 } ?? ""
        regionField.stringValue = region.isEmpty ? "us-east-1" : region
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

    /// The owning profile's env vars — threaded into every AWS-facing call,
    /// same convention CloudAccountsWindowController uses for `extraEnv`.
    private func extraEnv() -> [String: String] {
        owningProfile?.envVars.asDictionary() ?? [:]
    }

    // MARK: - Loading

    @objc func loadTemplates() {
        guard !region().isEmpty else { return }
        let (p, r, env) = (profile, region(), extraEnv())
        busy("Loading launch templates from \(r)…")
        service.runAsync({ try self.service.launchTemplates(profile: p, region: r, extraEnv: env) }) { [weak self] result in
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

    func loadVersions(for template: LaunchTemplate, thenSelect version: String? = nil) {
        let (p, r, env) = (profile, region(), extraEnv())
        busy("Loading versions of \(template.name)…")
        service.runAsync({ try self.service.launchTemplateVersions(profile: p, region: r, name: template.name, extraEnv: env) }) { [weak self] result in
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

    func loadVersionData(template: LaunchTemplate, version: String) {
        let (p, r, env) = (profile, region(), extraEnv())
        busy("Loading \(template.name) v\(version)…")
        service.runAsync({ try self.service.launchTemplateData(profile: p, region: r, name: template.name, version: version, extraEnv: env) }) { [weak self] result in
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

    func applyFieldFilter() {
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

    func updateApplyState() {
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

        let (p, r, env) = (profile, region(), extraEnv())
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
                setDefault: setDefault, fields: edits, extraEnv: env)
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

        let (p, r, env) = (profile, region(), extraEnv())
        busy("Rolling back default to v\(target)…")
        service.runAsync({ try self.service.setLaunchTemplateDefault(profile: p, region: r, name: template.name, version: String(target), extraEnv: env) }) { [weak self] result in
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

        let (p, r, env) = (profile, region(), extraEnv())
        busy("Deleting v\(version)…")
        service.runAsync({ try self.service.deleteLaunchTemplateVersions(profile: p, region: r, name: template.name, versions: [version], extraEnv: env) }) { [weak self] result in
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

}
