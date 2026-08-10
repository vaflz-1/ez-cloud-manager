import AppKit

extension CloudAccountsWindowController {
    /// Invalidates every async operation whose result is bound to the current
    /// detail form. The underlying mutation may still finish safely against
    /// its captured provider/name, but it must never rebind a newer editor.
    func beginEditorContextChange() {
        editorContextGeneration += 1
        profileLoadGeneration += 1
        pasteParseGeneration += 1
        connectionSaveGeneration += 1
    }

    func setEditorBaseline(provider: String, name: String, fields: [String: String]) {
        editorBaseline = ConnectionEditorBaseline(provider: provider, name: name, fields: fields)
    }

    func hasUnsavedConnectionChanges() -> Bool {
        let unparsedPaste = pasteView != nil
            && !pasteView.string.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && pasteView.string.trimmingCharacters(in: .whitespacesAndNewlines) != lastAutoParsedPaste
        guard let baseline = editorBaseline else { return unparsedPaste }
        let name = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        return baseline.provider != currentEditingProvider()
            || baseline.name != name
            || baseline.fields != fieldsDictionary()
            || unparsedPaste
    }

    func confirmDiscardConnectionChanges() -> Bool {
        commitActiveEdits()
        guard hasUnsavedConnectionChanges() else { return true }
        let target = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let alert = NSAlert()
        alert.messageText = "Discard unsaved connection changes?"
        alert.informativeText = target.isEmpty
            ? "The new connection draft has not been saved."
            : "Changes to \(target) have not been saved."
        alert.addButton(withTitle: "Keep Editing")
        alert.addButton(withTitle: "Discard Changes")
        guard alert.runModal() == .alertSecondButtonReturn else { return false }
        setEditorBaseline(
            provider: currentEditingProvider(),
            name: target,
            fields: fieldsDictionary()
        )
        lastAutoParsedPaste = pasteView.string.trimmingCharacters(in: .whitespacesAndNewlines)
        return true
    }

    /// Provider that owns the profile being edited: the selected profile's
    /// provider, or the provider chosen in the popup for a new profile.
    func currentEditingProvider() -> String {
        if selectedProfileName != nil { return selectedProvider }
        if let id = providerPopup?.selectedItem?.representedObject as? String { return id }
        return selectedProvider
    }

    // MARK: - Profile loading / sidebar model

    func refreshProfiles(selecting wantedName: String? = nil, provider wantedProvider: String? = nil) {
        connectionsLoadGeneration += 1
        let generation = connectionsLoadGeneration
        let env = profile.envVars.asDictionary()
        setStatus(profilesByProvider.isEmpty ? "Loading connections…" : "Refreshing connections…")
        service.runAsync({ try self.service.listConnections(extraEnv: env) }) { [weak self] result in
            guard let self, generation == self.connectionsLoadGeneration else { return }
            switch result {
            case .success(let response):
                self.applyConnectionsSnapshot(
                    response,
                    selecting: wantedName,
                    provider: wantedProvider
                )
            case .failure(let error):
                self.setStatus("Refresh failed — showing the last snapshot: \(error.localizedDescription)")
            }
        }
    }

    private func applyConnectionsSnapshot(
        _ response: ConnectionsListResponse,
        selecting wantedName: String?,
        provider wantedProvider: String?
    ) {
        // A Foundation/AppKit field editor can still contain the newest
        // keystrokes while its target model has the previous value. Commit
        // that editor before deciding whether an async refresh may rebind
        // the form, otherwise the completion can erase visible input.
        commitActiveEdits()
        let preserveDraft = hasUnsavedConnectionChanges()
        let snapshots = Dictionary(uniqueKeysWithValues: response.providers.map { ($0.provider, $0) })
        let infos = catalog.providers.isEmpty
            ? [ProviderInfo(id: "aws", displayName: "AWS", canActivate: false, activateLabel: nil)]
            : catalog.providers
        var failures: [String] = []
        for info in infos {
            guard let snapshot = snapshots[info.id] else {
                failures.append("\(info.displayName) missing")
                continue
            }
            if let error = snapshot.error, !error.isEmpty {
                failures.append("\(info.displayName) unavailable")
                if profilesByProvider[info.id] == nil {
                    profilesByProvider[info.id] = []
                }
                continue
            }
            profilesByProvider[info.id] = snapshot.profiles.sorted {
                $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
            }
            if let path = snapshot.path {
                pathsByProvider[info.id] = path
            }
        }
        rebuildSidebarRows()

        let provider = wantedProvider ?? selectedProvider
        let name = wantedName ?? selectedProfileName
        if let name, let idx = sidebarIndex(ofProfile: name, provider: provider) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
            if preserveDraft {
                updateProfileMode()
                let loadedCount = profilesByProvider.values.reduce(0) { $0 + $1.count }
                setStatus("Refreshed \(loadedCount) connection(s) · unsaved draft preserved")
            } else {
                loadProfile(provider: provider, name: name)
            }
        } else {
            if preserveDraft {
                updateProfileMode()
                let loadedCount = profilesByProvider.values.reduce(0) { $0 + $1.count }
                setStatus("Refreshed \(loadedCount) connection(s) · unsaved draft preserved")
                return
            }
            if selectedProfileName == nil
                && profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                clearDetailForNoSelection()
            }
            updateProfileMode()
            let loadedCount = profilesByProvider.values.reduce(0) { $0 + $1.count }
            let suffix = failures.isEmpty ? "" : " · \(failures.joined(separator: ", "))"
            setStatus("Loaded \(loadedCount) connection(s)\(suffix)")
        }
    }

    /// Rebuilds the sidebar rows from provider groups, applying the search
    /// text and this window's Cloud Accounts scoping settings (unless the
    /// profile shows all accounts) — see Profile.cloudAccountsSettings,
    /// edited in the Scope sheet (CloudAccountsWindowController+Scope.swift).
    func rebuildSidebarRows() {
        let query = (profileSearchField?.stringValue ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let scope = profile.cloudAccountsSettings
        let scoped = !scope.showAllAccounts
        var rows: [SidebarRow] = []
        for info in catalog.providers {
            let all = profilesByProvider[info.id] ?? []
            let visible = all.filter { summary in
                if scoped, !scope.accounts.contains(AccountRef(provider: info.id, account: summary.name)) {
                    return false
                }
                return query.isEmpty || summary.name.localizedCaseInsensitiveContains(query)
            }
            // Hide empty provider groups while filtering/account-scoped so
            // the list stays dense; show them when browsing everything (an
            // empty "Azure" header is the discoverable way to add one).
            if visible.isEmpty && (scoped || !query.isEmpty) { continue }
            rows.append(.header(provider: info.id, title: info.displayName, count: visible.count))
            rows.append(contentsOf: visible.map { .profile(provider: info.id, name: $0.name) })
        }
        sidebarRows = rows

        isRebuildingSidebar = true
        profilesTable.reloadData()
        if let name = selectedProfileName, let idx = sidebarIndex(ofProfile: name, provider: selectedProvider) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        }
        isRebuildingSidebar = false

        // The filter can hide the profile whose fields are on screen; leaving
        // them visible looks like phantom state — clear the detail, remember
        // what was selected, and bring it back the moment the filter relents.
        if let name = selectedProfileName, sidebarIndex(ofProfile: name, provider: selectedProvider) == nil {
            hiddenSelection = (selectedProvider, name)
            if hasUnsavedConnectionChanges() {
                setStatus("“\(name)” is hidden by the filter · unsaved draft preserved")
            } else {
                clearDetailForNoSelection()
                setStatus(query.isEmpty
                    ? "“\(name)” isn’t in this workspace’s scope — edit Visible Connections, or show all"
                    : "“\(name)” is hidden by the filter — clear the search to restore it")
            }
        } else if selectedProfileName != nil {
            hiddenSelection = nil
        } else if let hidden = hiddenSelection,
                  let idx = sidebarIndex(ofProfile: hidden.name, provider: hidden.provider) {
            hiddenSelection = nil
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        }
    }

    func sidebarIndex(ofProfile name: String, provider: String) -> Int? {
        sidebarRows.firstIndex {
            if case .profile(let p, let n) = $0 { return p == provider && n == name }
            return false
        }
    }

    func profileExists(_ name: String, provider: String) -> Bool {
        (profilesByProvider[provider] ?? []).contains { $0.name == name }
    }

    func loadProfile(provider: String, name: String) {
        beginEditorContextChange()
        let generation = profileLoadGeneration
        let env = profile.envVars.asDictionary()
        selectedProvider = provider
        selectedProfileName = name
        profileNameField.stringValue = name
        pasteView.string = ""
        lastAutoParsedPaste = ""
        fieldRows = []
        editorBaseline = nil
        collapsedSections = []
        reloadFieldsTable()
        updateVariablesSummary()
        updateProfileMode()
        saveButton.isEnabled = false
        profileModeLabel.stringValue = "Loading connection…"
        setStatus("Loading \(name)…")
        service.runAsync({ try self.service.get(provider: provider, name, extraEnv: env) }) { [weak self] result in
            guard let self, generation == self.profileLoadGeneration else { return }
            switch result {
            case .success(let profileResponse):
            hiddenSelection = nil // an explicit selection supersedes the remembered one
            selectedProvider = provider
            selectedProfileName = profileResponse.name
            profileNameField.stringValue = profileResponse.name
            pasteView.string = ""
            lastAutoParsedPaste = ""
            fieldRows = rows(from: profileResponse.fields, includeEmptyRecommended: true)
            setEditorBaseline(provider: provider, name: profileResponse.name, fields: fieldsDictionary())
            resetFieldCollapse()
            reloadFieldsTable()
            syncProviderPopup()
            updatePasteLabel()
            updatePasteViewPlaceholder()
            updateVariablesSummary()
            updateProfileMode()
            resetConnectionTestState() // never leave a stale result from a different account on screen
            setStatus("Loaded \(profileResponse.name) (\(catalog.providerDisplayName(provider)))")
            case .failure(let error):
                setStatus("Could not load \(name): \(error.localizedDescription)")
            }
        }
    }

    func clearDetailForNoSelection() {
        beginEditorContextChange()
        selectedProfileName = nil
        profileNameField.stringValue = ""
        pasteView.string = ""
        lastAutoParsedPaste = ""
        fieldRows = []
        editorBaseline = nil
        collapsedSections = []
        reloadFieldsTable()
        syncProviderPopup()
        updatePasteLabel()
        updatePasteViewPlaceholder()
        updateVariablesSummary()
        updateProfileMode()
        resetConnectionTestState()
    }

    /// Hides the paste-box hint the moment there's any text — see textDidChange below.
    func updatePasteViewPlaceholder() {
        pasteViewPlaceholder?.isHidden = !pasteView.string.isEmpty
    }

    // MARK: - Schema-driven field knowledge

    func spec(for key: String) -> FieldSpec? {
        catalog.schema(currentEditingProvider())?.spec(for: key)
    }

    /// Whether a field's value should be hidden in the UI. Schema-marked
    /// secrets are authoritative; for custom keys outside the schema a
    /// conservative name heuristic keeps obviously sensitive values masked.
    func isSecretKey(_ key: String) -> Bool {
        if let spec = spec(for: key) { return spec.isSecret }
        let k = key.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return k.contains("secret") || k.contains("token") || k.contains("password") || k.contains("private_key")
    }

    /// Masked representation of a secret value (empty stays empty).
    func masked(_ value: String) -> String {
        value.isEmpty ? "" : "••••••••"
    }

    /// Schema keys for the editing provider, in schema (UI) order.
    func recommendedKeys() -> [String] {
        catalog.schema(currentEditingProvider())?.fields.map { $0.key } ?? []
    }

    func section(for key: String) -> VarSection {
        guard let spec = spec(for: key) else { return .additional }
        return spec.isCommon ? .common : .advanced
    }

    func displayKey(_ key: String) -> String {
        spec(for: key)?.display ?? key
    }

    /// Muted example shown as placeholder for empty values (empty ≠ broken).
    func placeholderExample(for key: String) -> String {
        spec(for: key)?.placeholder ?? "Optional"
    }

    // MARK: - Variables grouping (displayItems presentation over fieldRows)

    /// Rebuilds `displayItems` from `fieldRows` honoring collapse state. Empty
    /// sections are omitted; a collapsed section shows only its header.
    func rebuildDisplayItems() {
        var out: [VarItem] = []
        for sec in VarSection.allCases {
            let idxs = fieldRows.indices.filter { section(for: fieldRows[$0].key) == sec }
            guard !idxs.isEmpty else { continue }
            out.append(.header(sec))
            if !collapsedSections.contains(sec) {
                out.append(contentsOf: idxs.map { VarItem.field($0) })
            }
        }
        displayItems = out
    }

    /// Default collapse: Advanced starts collapsed unless it already has a value.
    func resetFieldCollapse() {
        let advancedHasValue = fieldRows.contains {
            section(for: $0.key) == .advanced && !$0.value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        collapsedSections = advancedHasValue ? [] : [.advanced]
    }

    /// Rebuild the presentation and reload the fields table.
    func reloadFieldsTable() {
        rebuildDisplayItems()
        fieldsTable.reloadData()
    }

    /// Display-row index for a given `fieldRows` index, if currently visible.
    func displayIndex(ofField idx: Int) -> Int? {
        displayItems.firstIndex {
            if case .field(let i) = $0 { return i == idx }
            return false
        }
    }

    func rows(from fields: [String: String], includeEmptyRecommended: Bool = false) -> [FieldRow] {
        var rows: [FieldRow] = []
        let recommended = recommendedKeys()
        for key in recommended {
            if let value = fields[key] {
                rows.append(FieldRow(key: key, value: value))
            } else if includeEmptyRecommended {
                rows.append(FieldRow(key: key, value: ""))
            }
        }
        let extras = fields.keys.filter { !recommended.contains($0) }.sorted()
        for key in extras {
            rows.append(FieldRow(key: key, value: fields[key] ?? ""))
        }
        if rows.isEmpty {
            return standardEmptyRows()
        }
        return rows
    }

    func standardEmptyRows() -> [FieldRow] {
        recommendedKeys().map { FieldRow(key: $0, value: "") }
    }

    func fieldsDictionary() -> [String: String] {
        var fields: [String: String] = [:]
        for row in fieldRows {
            let key = row.key.trimmingCharacters(in: .whitespacesAndNewlines)
            let value = row.value.trimmingCharacters(in: .whitespacesAndNewlines)
            if !key.isEmpty {
                fields[key] = value
            }
        }
        return fields
    }

    // MARK: - Detail chrome (labels, popups, buttons)

    func rebuildProviderPopup() {
        guard let popup = providerPopup else { return }
        popup.removeAllItems()
        for info in catalog.providers {
            popup.addItem(withTitle: info.displayName)
            popup.lastItem?.representedObject = info.id
            popup.lastItem?.image = ProviderStyle.badge(info.id, height: 13)
        }
        syncProviderPopup()
    }

    /// Popup reflects the editing provider; it is only enabled while creating
    /// a new profile (a stored profile cannot move between clouds).
    func syncProviderPopup() {
        guard let popup = providerPopup else { return }
        if let idx = catalog.providers.firstIndex(where: { $0.id == selectedProvider }) {
            popup.selectItem(at: idx)
        }
        popup.isEnabled = (selectedProfileName == nil)
        updateActivateButton()
        updateExportButton()
        updateTestConnectionButton()
    }

    func updatePasteLabel() {
        pasteLabel?.stringValue = "PASTE \(catalog.providerDisplayName(currentEditingProvider()).uppercased()) CREDENTIALS OR CONFIG"
    }

    func updateActivateButton() {
        guard let button = activateButton else { return }
        let info = catalog.providerInfo(selectedProvider)
        let visible = (info?.canActivate ?? false) && selectedProfileName != nil
        button.isHidden = !visible
        button.toolTip = info?.activateLabel
    }

    func updateExportButton() {
        exportButton?.isEnabled = selectedProfileName != nil
    }

    /// Test Connection is always visible (never hidden per-provider — even
    /// azure, which doesn't implement Check yet, reports a clean "not
    /// supported" result rather than the button mysteriously disappearing,
    /// per docs/PLATFORM.md principle 6). It's only ENABLED once a saved
    /// account is selected.
    func updateTestConnectionButton() {
        let enabled = selectedProfileName != nil
        testConnectionButton?.isEnabled = enabled
        testConnectionButton?.toolTip = enabled ? nil : "Select a saved account to test its credentials"
    }

    func updateVariablesSummary() {
        if fieldRows.isEmpty {
            variablesSummaryLabel.stringValue = "Select a connection or click + to create one"
            fieldsEmptyHintLabel?.isHidden = true // sidebar hint ("select a profile") covers this state better
            return
        }
        let filled = fieldRows.filter { !$0.value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.count
        fieldsEmptyHintLabel?.isHidden = filled > 0
        let name = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if !name.isEmpty {
            variablesSummaryLabel.stringValue = "\(name): \(filled)/\(fieldRows.count) variables editable"
        } else {
            variablesSummaryLabel.stringValue = "\(filled)/\(fieldRows.count) variables ready to save"
        }
    }

    func updateProfileMode() {
        let name = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty && selectedProfileName == nil && fieldRows.isEmpty {
            profileModeLabel.stringValue = "No connection selected"
            saveButton.title = "Create Connection"
            saveButton.isEnabled = false
        } else if !name.isEmpty && profileExists(name, provider: currentEditingProvider()) {
            profileModeLabel.stringValue = "Updating existing connection"
            saveButton.title = "Save Changes"
            saveButton.isEnabled = !fieldRows.isEmpty && hasUnsavedConnectionChanges()
        } else {
            profileModeLabel.stringValue = "Creating new connection"
            saveButton.title = "Create Connection"
            saveButton.isEnabled = !name.isEmpty && !fieldRows.isEmpty && hasUnsavedConnectionChanges()
        }
        updateActivateButton()
        updateExportButton()
        updateTestConnectionButton()
    }

    func controlTextDidChange(_ obj: Notification) {
        let control = obj.object as? NSControl
        if control === profileNameField {
            updateProfileMode()
            updateVariablesSummary()
        } else if control === profileSearchField {
            rebuildSidebarRows()
        } else if control === guidedDeleteNewNameField {
            updateGuidedDeleteConfirmEnabled()
        }
    }

    func textDidChange(_ notification: Notification) {
        guard notification.object as? NSTextView == pasteView else {
            return
        }
        updatePasteViewPlaceholder()
        updateProfileMode()
        NSObject.cancelPreviousPerformRequests(withTarget: self, selector: #selector(autoParsePastedCredentials), object: nil)
        perform(#selector(autoParsePastedCredentials), with: nil, afterDelay: 0.25)
    }

    func setStatus(_ message: String) {
        statusLabel.stringValue = message
        if window?.isVisible == true {
            NSAccessibility.post(element: statusLabel as Any, notification: .valueChanged)
        }
    }

    func showError(_ message: String) {
        setStatus("Error")
        let alert = NSAlert()
        alert.messageText = Product.name
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }
}
