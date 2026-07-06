import AppKit

extension CloudAccountsWindowController {
    @objc func refreshTapped() {
        refreshProfiles()
    }

    @objc func sidebarSegment(_ sender: NSSegmentedControl) {
        sender.selectedSegment == 0 ? addProfile() : deleteProfile()
    }

    @objc func addProfile() {
        // Keep the provider of whatever group the user was looking at.
        selectedProfileName = nil
        profileNameField.stringValue = ""
        pasteView.string = ""
        lastAutoParsedPaste = ""
        fieldRows = standardEmptyRows()
        resetFieldCollapse()
        reloadFieldsTable()
        syncProviderPopup()
        updatePasteLabel()
        updateVariablesSummary()
        updateProfileMode()
        setStatus("New \(catalog.providerDisplayName(currentEditingProvider())) profile")
    }

    @objc func providerPopupChanged(_ sender: NSPopUpButton) {
        guard selectedProfileName == nil else { return }
        guard let id = sender.selectedItem?.representedObject as? String else { return }
        selectedProvider = id
        fieldRows = standardEmptyRows()
        resetFieldCollapse()
        reloadFieldsTable()
        updatePasteLabel()
        updateVariablesSummary()
        updateProfileMode()
        setStatus("New \(catalog.providerDisplayName(id)) profile")
    }

    @objc func deleteProfile() {
        let provider = currentEditingProvider()
        let name = selectedProfileName ?? profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            showError("Select a profile first.")
            return
        }

        let path = pathsByProvider[provider] ?? "the provider's store"
        let alert = NSAlert()
        alert.messageText = "Delete \(catalog.providerDisplayName(provider)) profile?"
        alert.informativeText = "Profile \"\(name)\" will be removed from \(path). A timestamped backup is created before writing."
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
            showError(error.localizedDescription)
        }
    }

    @objc func parsePastedCredentials() {
        if !applyParsedCredentialsFromPaste(force: true, userInitiated: true) {
            showError("No \(catalog.providerDisplayName(currentEditingProvider())) variables found in the pasted text.")
        }
    }

    @objc func autoParsePastedCredentials() {
        _ = applyParsedCredentialsFromPaste(force: false, userInitiated: false)
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
            guard !parsed.fields.isEmpty else {
                return false
            }
            lastAutoParsedPaste = text
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
                setStatus(userInitiated ? "Parsed \(parsed.fields.count) variable(s)" : "Auto-parsed \(parsed.fields.count) variable(s)")
            }
            return true
        } catch {
            if userInitiated {
                showError(error.localizedDescription)
            }
            return false
        }
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
    }

    @objc func removeVariable() {
        let disp = fieldsTable.selectedRow
        guard disp >= 0, disp < displayItems.count, case .field(let idx) = displayItems[disp] else {
            return
        }
        fieldRows.remove(at: idx)
        reloadFieldsTable()
        updateVariablesSummary()
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
        _ = applyParsedCredentialsFromPaste(force: false, userInitiated: false)

        let provider = currentEditingProvider()
        let name = profileNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            showError("Profile name is required.")
            return
        }

        let fields = fieldsDictionary()
        guard fields.values.contains(where: { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }) else {
            showError("Add at least one variable value.")
            return
        }

        do {
            let wasExisting = profileExists(name, provider: provider)
            if wasExisting, !confirmOverwrite(provider: provider, name: name, newFields: fields) {
                setStatus("Save cancelled")
                return
            }
            try service.save(provider: provider, name, fields: fields, extraEnv: profile.envVars.asDictionary())
            let savedCount = fields.values.filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }.count
            selectedProvider = provider
            refreshProfiles(selecting: name, provider: provider)
            setStatus(wasExisting ? "Updated \(name) · \(savedCount) field(s)" : "Created \(name) · \(savedCount) field(s)")
        } catch {
            showError(error.localizedDescription)
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

    /// Shows a readable, grouped diff of what will change on an existing profile
    /// and asks for confirmation. Returns true to proceed (or if nothing changed).
    func confirmOverwrite(provider: String, name: String, newFields: [String: String]) -> Bool {
        let current: [String: String]
        do {
            current = try service.get(provider: provider, name, extraEnv: profile.envVars.asDictionary()).fields
        } catch {
            return true // can't read current state; fall back to a plain save
        }

        let diff = diffGroups(old: current, new: newFields)
        let total = diff.added.count + diff.changed.count + diff.removed.count
        if total == 0 { return true }

        let path = pathsByProvider[provider] ?? "disk"
        let alert = NSAlert()
        alert.messageText = "Save changes to “\(name)”?"
        alert.informativeText = "\(total) change\(total == 1 ? "" : "s") to \(path) — a timestamped backup is written first."
        alert.accessoryView = diffAccessoryView(diff)
        alert.addButton(withTitle: "Save Changes")
        alert.addButton(withTitle: "Cancel")
        return alert.runModal() == .alertFirstButtonReturn
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

    /// A scrollable, monospaced, color-coded diff view — reused by the save
    /// confirmation, profile compare and Launch Template apply flows.
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
