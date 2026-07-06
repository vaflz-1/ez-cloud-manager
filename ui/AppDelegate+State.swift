import AppKit

extension AppDelegate {
    /// Canonical provider ordering for the sidebar: the big three first,
    /// anything registered later sorts after them.
    static let providerOrder = ["aws", "gcp", "azure"]

    /// Loads provider list + field schemas once at startup. Failure leaves the
    /// app usable with the AWS legacy assumptions (secret-name heuristics).
    func loadProviders() {
        do {
            var infos = try service.providers()
            infos.sort { lhs, rhs in
                let li = Self.providerOrder.firstIndex(of: lhs.id) ?? Int.max
                let ri = Self.providerOrder.firstIndex(of: rhs.id) ?? Int.max
                return li == ri ? lhs.id < rhs.id : li < ri
            }
            providers = infos
            for info in infos {
                schemas[info.id] = try? service.schema(provider: info.id)
            }
            rebuildProviderPopup()
        } catch {
            showError(error.localizedDescription)
        }
    }

    func providerInfo(_ id: String) -> ProviderInfo? {
        providers.first { $0.id == id }
    }

    func providerDisplayName(_ id: String) -> String {
        providerInfo(id)?.displayName ?? id.uppercased()
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
        var loadedCount = 0
        for info in providers.isEmpty ? [ProviderInfo(id: "aws", displayName: "AWS", canActivate: false, activateLabel: nil)] : providers {
            do {
                let response = try service.list(provider: info.id)
                profilesByProvider[info.id] = response.profiles.sorted {
                    $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
                }
                pathsByProvider[info.id] = response.path
                loadedCount += response.profiles.count
            } catch {
                // One broken backend (e.g. unreadable gcloud dir) must not
                // take down the others; surface it in status, keep going.
                setStatus("\(info.displayName): \(error.localizedDescription)")
                profilesByProvider[info.id] = []
            }
        }
        rebuildSidebarRows()

        let provider = wantedProvider ?? selectedProvider
        let name = wantedName ?? selectedProfileName
        if let name, let idx = sidebarIndex(ofProfile: name, provider: provider) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
            loadProfile(provider: provider, name: name)
        } else {
            if selectedProfileName == nil && profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                clearDetailForNoSelection()
            }
            updateProfileMode()
            setStatus("Loaded \(loadedCount) profile(s) across \(profilesByProvider.count) provider(s)")
        }
    }

    /// Rebuilds the sidebar rows from provider groups, applying the search
    /// text and the active workspace filter.
    func rebuildSidebarRows() {
        let query = (profileSearchField?.stringValue ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let workspace = activeWorkspace()
        var rows: [SidebarRow] = []
        for info in providers {
            let all = profilesByProvider[info.id] ?? []
            let visible = all.filter { summary in
                if let workspace, !workspace.members.contains(WorkspaceMember(provider: info.id, profile: summary.name)) {
                    return false
                }
                return query.isEmpty || summary.name.localizedCaseInsensitiveContains(query)
            }
            let tools = providerTools(info.id, matching: query)
            // Hide empty provider groups while filtering/workspace-scoped so
            // the list stays dense; show them when browsing everything (an
            // empty "Azure" header is the discoverable way to add one).
            if visible.isEmpty && tools.isEmpty && (workspace != nil || !query.isEmpty) { continue }
            rows.append(.header(provider: info.id, title: info.displayName, count: visible.count))
            rows.append(contentsOf: visible.map { .profile(provider: info.id, name: $0.name) })
            // A "TOOLS" subheader separates services from profiles so tools
            // don't read as just more profiles (skipped when there is nothing
            // above them to confuse with).
            if !tools.isEmpty && !visible.isEmpty {
                rows.append(.subheader("Tools"))
            }
            rows.append(contentsOf: tools)
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
            clearDetailForNoSelection()
            setStatus(query.isEmpty
                ? "“\(name)” isn’t in this workspace — switch workspace to edit it"
                : "“\(name)” is hidden by the filter — clear the search to restore it")
        } else if selectedProfileName == nil, let hidden = hiddenSelection,
                  let idx = sidebarIndex(ofProfile: hidden.name, provider: hidden.provider) {
            hiddenSelection = nil
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        }
    }

    /// Tool/service rows a provider contributes to the sidebar (the "internal
    /// apps" of each cloud). Search filters them like profiles.
    func providerTools(_ providerID: String, matching query: String) -> [SidebarRow] {
        var tools: [SidebarRow] = []
        if providerID == "aws" {
            tools.append(.tool(provider: "aws", id: "launch-templates",
                               title: "EC2 Launch Templates", symbol: "server.rack"))
        }
        guard !query.isEmpty else { return tools }
        return tools.filter {
            if case .tool(_, _, let title, _) = $0 { return title.localizedCaseInsensitiveContains(query) }
            return false
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
        do {
            let profile = try service.get(provider: provider, name)
            hiddenSelection = nil // an explicit selection supersedes the remembered one
            selectedProvider = provider
            selectedProfileName = profile.name
            profileNameField.stringValue = profile.name
            pasteView.string = ""
            lastAutoParsedPaste = ""
            fieldRows = rows(from: profile.fields, includeEmptyRecommended: true)
            resetFieldCollapse()
            reloadFieldsTable()
            syncProviderPopup()
            updatePasteLabel()
            updateVariablesSummary()
            updateProfileMode()
            setStatus("Loaded \(profile.name) (\(providerDisplayName(provider)))")
        } catch {
            showError(error.localizedDescription)
        }
    }

    func clearDetailForNoSelection() {
        selectedProfileName = nil
        profileNameField.stringValue = ""
        pasteView.string = ""
        lastAutoParsedPaste = ""
        fieldRows = []
        collapsedSections = []
        reloadFieldsTable()
        syncProviderPopup()
        updatePasteLabel()
        updateVariablesSummary()
        updateProfileMode()
    }

    // MARK: - Schema-driven field knowledge

    func spec(for key: String) -> FieldSpec? {
        schemas[currentEditingProvider()]?.spec(for: key)
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
        schemas[currentEditingProvider()]?.fields.map { $0.key } ?? []
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
        for info in providers {
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
        if let idx = providers.firstIndex(where: { $0.id == selectedProvider }) {
            popup.selectItem(at: idx)
        }
        popup.isEnabled = (selectedProfileName == nil)
        // The accent stripe ties the detail card back to the sidebar's
        // provider colors.
        profileCardStripe?.layer?.backgroundColor =
            ProviderStyle.color(currentEditingProvider()).withAlphaComponent(0.9).cgColor
        updateActivateButton()
        updateExportButton()
    }

    func updatePasteLabel() {
        pasteLabel?.stringValue = "PASTE \(providerDisplayName(currentEditingProvider()).uppercased()) CREDENTIALS OR CONFIG"
    }

    func updateActivateButton() {
        guard let button = activateButton else { return }
        let info = providerInfo(selectedProvider)
        let visible = (info?.canActivate ?? false) && selectedProfileName != nil
        button.isHidden = !visible
        button.toolTip = info?.activateLabel
    }

    func updateExportButton() {
        exportButton?.isEnabled = selectedProfileName != nil
    }

    func updateVariablesSummary() {
        if fieldRows.isEmpty {
            variablesSummaryLabel.stringValue = "Select a profile or click + to create one"
            return
        }
        let filled = fieldRows.filter { !$0.value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.count
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
            profileModeLabel.stringValue = "No profile selected"
            saveButton.title = "Create profile"
            saveButton.isEnabled = false
        } else if !name.isEmpty && profileExists(name, provider: currentEditingProvider()) {
            profileModeLabel.stringValue = "Updating existing profile"
            saveButton.title = "Update profile"
            saveButton.isEnabled = !fieldRows.isEmpty
        } else {
            profileModeLabel.stringValue = "Creating new profile"
            saveButton.title = "Create profile"
            saveButton.isEnabled = !name.isEmpty && !fieldRows.isEmpty
        }
        updateActivateButton()
        updateExportButton()
    }

    func controlTextDidChange(_ obj: Notification) {
        let control = obj.object as? NSControl
        if control === profileNameField {
            updateProfileMode()
            updateVariablesSummary()
        } else if control === profileSearchField {
            rebuildSidebarRows()
        }
    }

    func textDidChange(_ notification: Notification) {
        guard notification.object as? NSTextView == pasteView else {
            return
        }
        NSObject.cancelPreviousPerformRequests(withTarget: self, selector: #selector(autoParsePastedCredentials), object: nil)
        perform(#selector(autoParsePastedCredentials), with: nil, afterDelay: 0.25)
    }

    func setStatus(_ message: String) {
        statusLabel.stringValue = message
    }

    func showError(_ message: String) {
        setStatus("Error")
        let alert = NSAlert()
        alert.messageText = "EZ Cloud Manager"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }
}
