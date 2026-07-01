import AppKit

extension AppDelegate {
    func refreshProfiles(selecting wantedName: String? = nil) {
        do {
            let response = try service.list()
            credentialsPath = response.path
            allProfiles = response.profiles.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            applyProfileFilter()

            let nameToSelect = wantedName ?? selectedProfileName
            if let name = nameToSelect, let idx = profiles.firstIndex(where: { $0.name == name }) {
                profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
                loadProfile(name)
            } else {
                if selectedProfileName == nil && profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    clearDetailForNoSelection()
                }
                updateProfileMode()
                setStatus("Loaded \(allProfiles.count) profile(s) from \(credentialsPath)")
            }
        } catch {
            showError(error.localizedDescription)
        }
    }

    /// Filters `allProfiles` by the search field into the visible `profiles`.
    func applyProfileFilter() {
        let query = (profileSearchField?.stringValue ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        if query.isEmpty {
            profiles = allProfiles
        } else {
            profiles = allProfiles.filter { $0.name.localizedCaseInsensitiveContains(query) }
        }
        profilesTable.reloadData()
        if let name = selectedProfileName, let idx = profiles.firstIndex(where: { $0.name == name }) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        }
    }

    /// Whether a field's value should be hidden in the UI.
    func isSecretKey(_ key: String) -> Bool {
        secretKeys.contains(key.trimmingCharacters(in: .whitespacesAndNewlines).lowercased())
    }

    /// Masked representation of a secret value (empty stays empty).
    func masked(_ value: String) -> String {
        value.isEmpty ? "" : "••••••••"
    }

    // MARK: - Variables grouping (displayItems presentation over fieldRows)

    func section(for key: String) -> VarSection {
        let k = key.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if commonKeys.contains(k) { return .common }
        if recommendedKeys.contains(k) { return .advanced }
        return .additional
    }

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

    /// Muted example shown as placeholder for empty values (empty ≠ broken).
    func placeholderExample(for key: String) -> String {
        switch key.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "aws_access_key_id":       return "AKIA…"
        case "aws_secret_access_key":   return "Not set — click the eye to add"
        case "aws_session_token":       return "Not set — click the eye to add"
        case "region":                  return "us-east-1"
        case "output":                  return "json"
        case "role_arn":                return "arn:aws:iam::123456789012:role/Name"
        case "source_profile":          return "default"
        case "mfa_serial":              return "arn:aws:iam::123456789012:mfa/user"
        case "duration_seconds":        return "3600"
        case "role_session_name":       return "my-session"
        case "external_id":             return "unique-id"
        case "web_identity_token_file": return "/path/to/token"
        case "endpoint_url":            return "https://…"
        case "cli_pager":               return "(empty to disable)"
        case "retry_mode":              return "standard"
        case "max_attempts":            return "3"
        case "sts_regional_endpoints":  return "regional"
        case "sso_session":             return "my-sso"
        case "sso_start_url":           return "https://my.awsapps.com/start"
        case "sso_region":              return "us-east-1"
        case "sso_account_id":          return "123456789012"
        case "sso_role_name":           return "PowerUserAccess"
        case "credential_process":      return "/path/to/helper"
        case "credential_source":       return "Ec2InstanceMetadata"
        case "ca_bundle":               return "/path/to/ca.pem"
        default:                        return "Optional"
        }
    }

    func loadProfile(_ name: String) {
        do {
            let profile = try service.get(name)
            selectedProfileName = profile.name
            profileNameField.stringValue = profile.name
            pasteView.string = ""
            lastAutoParsedPaste = ""
            fieldRows = rows(from: profile.fields, includeEmptyRecommended: true)
            resetFieldCollapse()
            reloadFieldsTable()
            updateVariablesSummary()
            updateProfileMode()
            setStatus("Loaded \(profile.name)")
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
        updateVariablesSummary()
        updateProfileMode()
    }

    func rows(from fields: [String: String], includeEmptyRecommended: Bool = false) -> [FieldRow] {
        var rows: [FieldRow] = []
        for key in recommendedKeys {
            if let value = fields[key] {
                rows.append(FieldRow(key: key, value: value))
            } else if includeEmptyRecommended {
                rows.append(FieldRow(key: key, value: ""))
            }
        }
        let extras = fields.keys.filter { !recommendedKeys.contains($0) }.sorted()
        for key in extras {
            rows.append(FieldRow(key: key, value: fields[key] ?? ""))
        }
        if rows.isEmpty {
            return standardEmptyRows()
        }
        return rows
    }

    func standardEmptyRows() -> [FieldRow] {
        recommendedKeys.map { FieldRow(key: $0, value: "") }
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

    func displayKey(_ key: String) -> String {
        switch key.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "aws_access_key_id":
            return "AWS_ACCESS_KEY_ID"
        case "aws_secret_access_key":
            return "AWS_SECRET_ACCESS_KEY"
        case "aws_session_token":
            return "AWS_SESSION_TOKEN"
        case "region":
            return "AWS_DEFAULT_REGION"
        case "output":
            return "AWS_DEFAULT_OUTPUT"
        case "ca_bundle":
            return "AWS_CA_BUNDLE"
        case "role_arn":
            return "AWS_ROLE_ARN / role_arn"
        case "source_profile":
            return "source_profile"
        case "mfa_serial":
            return "mfa_serial"
        case "duration_seconds":
            return "duration_seconds"
        case "credential_process":
            return "credential_process"
        case "credential_source":
            return "credential_source"
        case "role_session_name":
            return "AWS_ROLE_SESSION_NAME"
        case "external_id":
            return "external_id"
        case "web_identity_token_file":
            return "AWS_WEB_IDENTITY_TOKEN_FILE"
        case "endpoint_url":
            return "AWS_ENDPOINT_URL"
        case "cli_pager":
            return "AWS_CLI_PAGER"
        case "retry_mode":
            return "AWS_RETRY_MODE"
        case "max_attempts":
            return "AWS_MAX_ATTEMPTS"
        case "sts_regional_endpoints":
            return "AWS_STS_REGIONAL_ENDPOINTS"
        case "sso_session":
            return "sso_session"
        case "sso_start_url":
            return "sso_start_url"
        case "sso_region":
            return "sso_region"
        case "sso_account_id":
            return "sso_account_id"
        case "sso_role_name":
            return "sso_role_name"
        case "":
            return ""
        default:
            return key
        }
    }

    func profileExists(_ name: String) -> Bool {
        profiles.contains { $0.name == name }
    }

    func updateProfileMode() {
        let name = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if name.isEmpty && selectedProfileName == nil && fieldRows.isEmpty {
            profileModeLabel.stringValue = "No profile selected"
            saveButton.title = "Create profile"
            saveButton.isEnabled = false
        } else if !name.isEmpty && profileExists(name) {
            profileModeLabel.stringValue = "Updating existing profile"
            saveButton.title = "Update profile"
            saveButton.isEnabled = !fieldRows.isEmpty
        } else {
            profileModeLabel.stringValue = "Creating new profile"
            saveButton.title = "Create profile"
            saveButton.isEnabled = !name.isEmpty && !fieldRows.isEmpty
        }
    }

    func controlTextDidChange(_ obj: Notification) {
        let control = obj.object as? NSControl
        if control === profileNameField {
            updateProfileMode()
            updateVariablesSummary()
        } else if control === profileSearchField {
            applyProfileFilter()
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
