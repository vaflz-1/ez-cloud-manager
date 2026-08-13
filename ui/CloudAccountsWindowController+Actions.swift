import AppKit

extension CloudAccountsWindowController {
    @objc func refreshTapped() {
        guard confirmDiscardConnectionChanges() else { return }
        refreshProfiles()
    }

    @objc func sidebarSegment(_ sender: NSSegmentedControl) {
        sender.selectedSegment == 0 ? addProfile() : deleteProfile()
    }

    @objc func addProfile() {
        guard confirmDiscardConnectionChanges() else { return }
        beginEditorContextChange()
        // Keep the provider of whatever group the user was looking at.
        selectedProfileName = nil
        profileNameField.stringValue = ""
        pasteView.string = ""
        lastAutoParsedPaste = ""
        fieldRows = standardEmptyRows()
        setEditorBaseline(provider: currentEditingProvider(), name: "", fields: fieldsDictionary())
        resetFieldCollapse()
        reloadFieldsTable()
        syncProviderPopup()
        updatePasteLabel()
        updatePasteViewPlaceholder()
        updateVariablesSummary()
        updateProfileMode()
        setStatus("New \(catalog.providerDisplayName(currentEditingProvider())) connection")
    }

    @objc func providerPopupChanged(_ sender: NSPopUpButton) {
        guard selectedProfileName == nil else { return }
        guard let id = sender.selectedItem?.representedObject as? String else { return }
        // NSPopUpButton has already changed its selected item before sending
        // the action. Restore the previous provider while evaluating dirty
        // state so a pristine new form does not look modified merely because
        // the popup moved.
        syncProviderPopup()
        guard confirmDiscardConnectionChanges() else {
            return
        }
        beginEditorContextChange()
        selectedProvider = id
        syncProviderPopup()
        fieldRows = standardEmptyRows()
        setEditorBaseline(provider: id, name: "", fields: fieldsDictionary())
        resetFieldCollapse()
        reloadFieldsTable()
        updatePasteLabel()
        updateVariablesSummary()
        updateProfileMode()
        setStatus("New \(catalog.providerDisplayName(id)) connection")
    }

    @objc func deleteProfile() {
        let provider = currentEditingProvider()
        let name = selectedProfileName ?? profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            showError("Select a connection first.")
            return
        }
        if profileSummary(name, provider: provider)?.isReadOnly == true {
            showError("This connection is managed by the provider CLI. Change or remove it there, then Refresh Connections.")
            return
        }

        // Pre-check: some providers (gcp today) refuse to delete their own
        // active/default entry. Route straight to the guided sheet BEFORE
        // even attempting the delete, rather than only reacting to the
        // failure — docs/PLATFORM.md principle 6 (guided flows, not bare
        // errors). The reactive catch below stays as a safety net for races
        // (e.g. something else activated a different config between load
        // and delete).
        if catalog.providerInfo(provider)?.canActivate == true,
           profilesByProvider[provider]?.first(where: { $0.name == name })?.isActive == true {
            presentGuidedGcpDeleteSheet(name: name)
            return
        }

        let path = pathsByProvider[provider] ?? "the provider's store"
        let alert = NSAlert()
        alert.messageText = "Delete \(catalog.providerDisplayName(provider)) connection?"
        alert.informativeText = "Connection \"\(name)\" will be removed from \(path). A timestamped backup is created before writing."
        alert.addButton(withTitle: "Delete")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else {
            return
        }

        do {
            try service.delete(provider: provider, name, extraEnv: profile.envVars.asDictionary())
            selectedProfileName = nil
            refreshProfiles()
            clearDetailForNoSelection()
            setStatus("Deleted \(name)")
        } catch {
            // Safety net for the pre-check above (see +GuidedDelete.swift).
            if provider == "gcp", error.localizedDescription.contains("is the active gcloud configuration") {
                presentGuidedGcpDeleteSheet(name: name)
                return
            }
            showError(error.localizedDescription)
        }
    }

    @objc func parsePastedCredentials() {
        parsePastedCredentialsAsync(force: true, userInitiated: true)
    }

    @objc func autoParsePastedCredentials() {
        parsePastedCredentialsAsync(force: false, userInitiated: false)
    }

    private func parsePastedCredentialsAsync(force: Bool, userInitiated: Bool) {
        let text = pasteView.string.trimmingCharacters(in: .whitespacesAndNewlines)
        let provider = currentEditingProvider()
        guard !text.isEmpty,
              (force || text != lastAutoParsedPaste),
              looksParseable(text)
        else {
            if userInitiated {
                showError("No \(catalog.providerDisplayName(provider)) variables found in the pasted text.")
            }
            return
        }

        pasteParseGeneration += 1
        let generation = pasteParseGeneration
        let env = profile.envVars.asDictionary()
        if userInitiated { setStatus("Reading pasted configuration…") }
        service.runAsync({ try self.service.parse(provider: provider, text, extraEnv: env) }) { [weak self] result in
            guard let self,
                  generation == self.pasteParseGeneration,
                  self.currentEditingProvider() == provider,
                  self.pasteView.string.trimmingCharacters(in: .whitespacesAndNewlines) == text
            else { return }
            switch result {
            case .success(let parsed):
                if !self.applyParsedResponse(parsed, sourceText: text, userInitiated: userInitiated), userInitiated {
                    self.showError("No \(self.catalog.providerDisplayName(provider)) variables found in the pasted text.")
                }
            case .failure(let error):
                if userInitiated { self.showError(error.localizedDescription) }
            }
        }
    }

    func applyParsedCredentialsFromPaste(force: Bool, userInitiated: Bool) -> Bool {
        let text = pasteView.string.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else {
            return false
        }
        guard force || text != lastAutoParsedPaste else {
            return !fieldRows.isEmpty
        }
        guard looksParseable(text) else {
            return false
        }

        do {
            let parsed = try service.parse(provider: currentEditingProvider(), text, extraEnv: profile.envVars.asDictionary())
            return applyParsedResponse(parsed, sourceText: text, userInitiated: userInitiated)
        } catch {
            if userInitiated {
                showError(error.localizedDescription)
            }
            return false
        }
    }

    private func applyParsedResponse(
        _ parsed: ParseResponse,
        sourceText: String,
        userInitiated: Bool
    ) -> Bool {
        guard !parsed.fields.isEmpty else { return false }
        lastAutoParsedPaste = sourceText
        if let name = parsed.profileName, !name.isEmpty, profileNameField.stringValue.isEmpty {
            profileNameField.stringValue = name
        }
        fieldRows = rows(from: parsed.fields, includeEmptyRecommended: true)
        resetFieldCollapse()
        reloadFieldsTable()
        updateVariablesSummary()
        updateProfileMode()
        // Parser notes are security-relevant (e.g. "private key not
        // imported") — they win over the generic success message.
        if let notes = parsed.notes, !notes.isEmpty {
            setStatus(notes.joined(separator: " "))
        } else {
            setStatus(userInitiated
                ? "Imported \(parsed.fields.count) value(s)"
                : "Imported \(parsed.fields.count) value(s) from paste")
        }
        return true
    }

    func looksParseable(_ text: String) -> Bool {
        text.contains("=") || text.contains("[") || text.contains("{")
            || text.localizedCaseInsensitiveContains("AWS_")
            || text.localizedCaseInsensitiveContains("AZURE_")
            || text.localizedCaseInsensitiveContains("ARM_")
            || text.localizedCaseInsensitiveContains("CLOUDSDK_")
            || text.localizedCaseInsensitiveContains("GOOGLE_")
    }

    @objc func addVariable() {
        guard !selectedConnectionIsReadOnly() else {
            showError("This connection is managed by the provider CLI and cannot be edited here.")
            return
        }
        fieldRows.append(FieldRow(key: "", value: ""))
        let newIdx = fieldRows.count - 1
        reloadFieldsTable()
        if let disp = displayIndex(ofField: newIdx) {
            fieldsTable.selectRowIndexes(IndexSet(integer: disp), byExtendingSelection: false)
            fieldsTable.scrollRowToVisible(disp)
            if let keyField = fieldsTable.view(atColumn: 0, row: disp, makeIfNecessary: true) as? NSTextField {
                window?.makeFirstResponder(keyField)
            }
        }
        updateVariablesSummary()
        updateProfileMode()
    }

    @objc func removeVariable() {
        guard !selectedConnectionIsReadOnly() else {
            showError("This connection is managed by the provider CLI and cannot be edited here.")
            return
        }
        let disp = fieldsTable.selectedRow
        guard disp >= 0, disp < displayItems.count, case .field(let idx) = displayItems[disp] else {
            return
        }
        fieldRows.remove(at: idx)
        reloadFieldsTable()
        updateVariablesSummary()
        updateProfileMode()
    }

    @objc func toggleSection(_ sender: NSButton) {
        guard let sec = VarSection(rawValue: sender.identifier?.rawValue ?? "") else { return }
        if collapsedSections.contains(sec) {
            collapsedSections.remove(sec)
        } else {
            collapsedSections.insert(sec)
        }
        reloadFieldsTable()
    }

    @objc func saveProfile() {
        commitActiveEdits()
        if selectedConnectionIsReadOnly() {
            showError("This connection is managed by the provider CLI and cannot be edited here.")
            return
        }
        _ = applyParsedCredentialsFromPaste(force: false, userInitiated: false)

        let provider = currentEditingProvider()
        let name = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            showError("Connection name is required.")
            return
        }

        let fields = fieldsDictionary()
        guard fields.values.contains(where: { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }) else {
            showError("Add at least one variable value.")
            return
        }

        let wasExisting = profileExists(name, provider: provider)
        let updatesLoadedConnection = editorBaseline?.provider == provider
            && editorBaseline?.name == name
        let expectedFields = updatesLoadedConnection ? editorBaseline?.persistedFields : nil
        let expectAbsent = !updatesLoadedConnection
        let env = profile.envVars.asDictionary()
        connectionSaveGeneration += 1
        let generation = connectionSaveGeneration
        let editorGeneration = editorContextGeneration
        saveButton.isEnabled = false
        setStatus("Saving \(name)…")
        service.runAsync({
            try self.service.save(
                provider: provider,
                name,
                fields: fields,
                expectedFields: expectedFields,
                expectAbsent: expectAbsent,
                extraEnv: env
            )
        }) { [weak self] result in
            guard let self,
                  generation == self.connectionSaveGeneration,
                  editorGeneration == self.editorContextGeneration
            else { return }
            switch result {
            case .success:
            let savedCount = fields.values.filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.count
            selectedProvider = provider
            selectedProfileName = name
            setEditorBaseline(provider: provider, name: name, fields: fields)
            var summaries = profilesByProvider[provider] ?? []
            let keys = fields
                .filter { !$0.value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
                .map(\.key)
                .sorted()
            if let index = summaries.firstIndex(where: { $0.name == name }) {
                summaries[index] = ProfileSummary(name: name, keys: keys, active: summaries[index].active)
            } else {
                summaries.append(ProfileSummary(name: name, keys: keys, active: nil))
            }
            profilesByProvider[provider] = summaries.sorted {
                $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
            }
            rebuildSidebarRows()
            if let index = sidebarIndex(ofProfile: name, provider: provider) {
                profilesTable.selectRowIndexes(IndexSet(integer: index), byExtendingSelection: false)
            }
            updateProfileMode()
            setStatus(wasExisting ? "Updated \(name) · \(savedCount) field(s)" : "Created \(name) · \(savedCount) field(s)")
            case .failure(CredentialsService.ServiceError.connectionConflict):
                updateProfileMode()
                setStatus("Save blocked · \(name) changed outside this editor")
                showError("\(name) changed after you opened it. Your draft is preserved. Refresh the connections, review the newer values, then save again.")
            case .failure(let error):
                updateProfileMode()
                showError(error.localizedDescription)
            }
        }
    }

    /// Copies the selected row's real value (even if masked in the table) to the
    /// clipboard — the intended way to hand a secret to another app.
    @objc func copyFieldValue() {
        let disp = fieldsTable.selectedRow
        guard disp >= 0, disp < displayItems.count, case .field(let idx) = displayItems[disp] else {
            showError("Select a variable row to copy its value.")
            return
        }
        let field = fieldRows[idx]
        guard !field.value.isEmpty else {
            setStatus("Nothing to copy")
            return
        }
        copyConcealed(field.value)
        setStatus("Copied \(displayKey(field.key))")
    }

    /// Copies text flagged as concealed so clipboard-history managers (Paste,
    /// Maccy, Alfred, …) skip logging it. Every copy that can contain secret
    /// material must go through here.
    func copyConcealed(_ text: String) {
        let pasteboard = NSPasteboard.general
        let concealed = NSPasteboard.PasteboardType("org.nspasteboard.ConcealedType")
        pasteboard.clearContents()
        pasteboard.declareTypes([.string, concealed], owner: nil)
        pasteboard.setString(text, forType: .string)
        pasteboard.setString(text, forType: concealed)
    }

    typealias FieldDiff = (added: [(String, String)], changed: [(String, String, String)], removed: [String])

    /// Groups the field diff (secrets masked; empty new value = removed).
    func diffGroups(old: [String: String], new: [String: String]) -> FieldDiff {
        var keys = Set(old.keys)
        new.keys.forEach { keys.insert($0) }

        func shown(_ key: String, _ value: String) -> String {
            isSecretKey(key) ? masked(value) : value
        }

        var added: [(String, String)] = []
        var changed: [(String, String, String)] = []
        var removed: [String] = []
        for key in keys.sorted() {
            let before = old[key] ?? ""
            let after = (new[key] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if before == after { continue }
            if before.isEmpty {
                added.append((key, shown(key, after)))
            } else if after.isEmpty {
                removed.append(key)
            } else {
                changed.append((key, shown(key, before), shown(key, after)))
            }
        }
        return (added, changed, removed)
    }

    /// A scrollable, monospaced, color-coded diff view used by profile
    /// compare (Launch Templates owns a separate diff renderer).
    func diffAccessoryView(_ diff: FieldDiff) -> NSView {
        let mono = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
        let sectionFont = NSFont.systemFont(ofSize: 10, weight: .semibold)
        let out = NSMutableAttributedString()

        let head = NSMutableParagraphStyle(); head.paragraphSpacingBefore = 8
        let line = NSMutableParagraphStyle(); line.lineSpacing = 2

        func section(_ title: String) {
            let attrs: [NSAttributedString.Key: Any] = [
                .font: sectionFont, .foregroundColor: NSColor.secondaryLabelColor,
                .kern: 0.6, .paragraphStyle: out.length > 0 ? head : line
            ]
            out.append(NSAttributedString(string: title + "\n", attributes: attrs))
        }
        func row(_ s: String, _ color: NSColor) {
            out.append(NSAttributedString(string: s + "\n",
                attributes: [.font: mono, .foregroundColor: color, .paragraphStyle: line]))
        }

        if !diff.changed.isEmpty {
            section("CHANGED")
            for (k, o, n) in diff.changed { row("    \(k):  \(o)  →  \(n)", .labelColor) }
        }
        if !diff.added.isEmpty {
            section("ADDED")
            for (k, v) in diff.added { row("  +  \(k):  \(v)", .systemGreen) }
        }
        if !diff.removed.isEmpty {
            section("REMOVED")
            for k in diff.removed { row("  −  \(k)", .systemRed) }
        }

        let textView = NSTextView()
        textView.isEditable = false
        textView.drawsBackground = true
        textView.backgroundColor = .textBackgroundColor
        textView.textContainerInset = NSSize(width: 12, height: 12)
        textView.textStorage?.setAttributedString(out)

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .lineBorder
        scroll.documentView = textView
        scroll.translatesAutoresizingMaskIntoConstraints = false
        let rows = diff.added.count + diff.changed.count + diff.removed.count
        let sections = (diff.changed.isEmpty ? 0 : 1) + (diff.added.isEmpty ? 0 : 1) + (diff.removed.isEmpty ? 0 : 1)
        let contentH = CGFloat(rows) * 18 + CGFloat(sections) * 22 + 28
        NSLayoutConstraint.activate([
            scroll.widthAnchor.constraint(equalToConstant: 430),
            scroll.heightAnchor.constraint(equalToConstant: min(320, max(96, contentH)))
        ])
        return scroll
    }

    func commitActiveEdits() {
        window?.makeFirstResponder(nil)
        fieldsTable.window?.makeFirstResponder(nil)
        fieldsTable.validateEditing()
        profileNameField.validateEditing()
    }
}
