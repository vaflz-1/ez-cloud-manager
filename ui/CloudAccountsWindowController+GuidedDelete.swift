import AppKit

/// Guided recovery for gcloud's one delete refusal: you cannot delete the
/// active configuration (internal/gcpcreds.Delete). Per docs/PLATFORM.md
/// principle 6 ("errors are guided flows"), this offers the unblocking
/// action in place — activate another configuration, or create a new one,
/// then delete — rather than a bare error dialog. Triggered by a PRE-CHECK
/// in deleteProfile() (+Actions.swift, via ProfileSummary.isActive) before
/// even attempting delete; the reactive error-string catch there stays as a
/// safety net for races (something else activated a different config
/// between load and delete).
extension CloudAccountsWindowController {
    /// Marks the popup's "create a new configuration" row, distinguishing it
    /// from a row that names a real, existing configuration.
    private static let createNewMarker = "__create_new__"

    func presentGuidedGcpDeleteSheet(name: String) {
        guard let parent = window else { return }
        guidedDeleteTargetName = name
        let others = (profilesByProvider["gcp"] ?? []).map(\.name).filter { $0 != name }
        guidedDeleteForcedCreate = others.isEmpty

        let sheet = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 420, height: 230),
            styleMask: [.titled],
            backing: .buffered, defer: false)
        guidedDeleteWindow = sheet

        let content = NSView()
        sheet.contentView = content

        let titleLabel = NSTextField(wrappingLabelWithString: "“\(name)” is the active gcloud configuration")
        titleLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        titleLabel.translatesAutoresizingMaskIntoConstraints = false

        let bodyText = guidedDeleteForcedCreate
            ? "This is your only gcloud configuration. Create another to replace it."
            : "You can't delete the configuration gcloud is currently using. Pick another to activate first — EZ Cloud Manager will switch to it, then delete “\(name)”."
        let bodyLabel = NSTextField(wrappingLabelWithString: bodyText)
        bodyLabel.font = .systemFont(ofSize: 11)
        bodyLabel.textColor = .secondaryLabelColor
        bodyLabel.translatesAutoresizingMaskIntoConstraints = false

        let popupCaption = NSTextField(labelWithString: "Activate instead:")
        popupCaption.font = .systemFont(ofSize: 11, weight: .medium)

        let popup = NSPopUpButton()
        popup.target = self
        popup.action = #selector(guidedDeletePopupChanged(_:))
        guidedDeletePopup = popup
        for other in others {
            popup.addItem(withTitle: other)
        }
        if !others.isEmpty {
            popup.menu?.addItem(.separator())
        }
        let createItem = NSMenuItem(title: "＋ Create new configuration…", action: nil, keyEquivalent: "")
        createItem.representedObject = Self.createNewMarker
        popup.menu?.addItem(createItem)
        if guidedDeleteForcedCreate {
            popup.select(createItem)
            popup.isEnabled = false
        }

        let popupRow = NSStackView(views: [popupCaption, popup])
        popupRow.orientation = .horizontal
        popupRow.spacing = 8

        let newNameField = NSTextField()
        newNameField.placeholderString = "new-configuration-name"
        newNameField.delegate = self
        guidedDeleteNewNameField = newNameField
        newNameField.isHidden = !guidedDeleteForcedCreate

        let errorLabel = NSTextField(wrappingLabelWithString: "")
        errorLabel.font = .systemFont(ofSize: 11)
        errorLabel.textColor = .systemRed
        guidedDeleteErrorLabel = errorLabel

        // Vertical stack so a hidden newNameField (an existing configuration
        // is selected) or an empty errorLabel (no failure yet) collapse
        // cleanly, rather than a manual constraint chain leaving a gap.
        let fieldsStack = NSStackView(views: [popupRow, newNameField, errorLabel])
        fieldsStack.orientation = .vertical
        fieldsStack.alignment = .leading
        fieldsStack.spacing = 10
        fieldsStack.translatesAutoresizingMaskIntoConstraints = false

        let spinner = NSProgressIndicator()
        spinner.style = .spinning
        spinner.controlSize = .small
        spinner.isDisplayedWhenStopped = false
        spinner.translatesAutoresizingMaskIntoConstraints = false
        guidedDeleteSpinner = spinner

        let cancelButton = NSButton(title: "Cancel", target: self, action: #selector(guidedDeleteCancel))
        cancelButton.bezelStyle = .rounded
        cancelButton.translatesAutoresizingMaskIntoConstraints = false
        guidedDeleteCancelButton = cancelButton

        let confirmButton = NSButton(
            title: guidedDeleteForcedCreate ? "Create, Switch & Delete" : "Switch & Delete",
            target: self, action: #selector(guidedDeleteConfirm))
        confirmButton.bezelStyle = .rounded
        confirmButton.keyEquivalent = "\r"
        confirmButton.translatesAutoresizingMaskIntoConstraints = false
        guidedDeleteConfirmButton = confirmButton

        for v in [titleLabel, bodyLabel, fieldsStack, spinner, cancelButton, confirmButton] {
            content.addSubview(v)
        }

        NSLayoutConstraint.activate([
            titleLabel.topAnchor.constraint(equalTo: content.topAnchor, constant: 20),
            titleLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            titleLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),

            bodyLabel.topAnchor.constraint(equalTo: titleLabel.bottomAnchor, constant: 8),
            bodyLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            bodyLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),

            fieldsStack.topAnchor.constraint(equalTo: bodyLabel.bottomAnchor, constant: 16),
            fieldsStack.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            fieldsStack.trailingAnchor.constraint(lessThanOrEqualTo: content.trailingAnchor, constant: -20),
            newNameField.leadingAnchor.constraint(equalTo: fieldsStack.leadingAnchor),
            newNameField.trailingAnchor.constraint(equalTo: fieldsStack.trailingAnchor),
            errorLabel.leadingAnchor.constraint(equalTo: fieldsStack.leadingAnchor),
            errorLabel.trailingAnchor.constraint(equalTo: fieldsStack.trailingAnchor),

            spinner.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            spinner.centerYAnchor.constraint(equalTo: cancelButton.centerYAnchor),

            cancelButton.trailingAnchor.constraint(equalTo: confirmButton.leadingAnchor, constant: -10),
            cancelButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -20),
            confirmButton.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),
            confirmButton.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -20)
        ])

        updateGuidedDeleteConfirmEnabled()
        parent.beginSheet(sheet, completionHandler: nil)
    }

    @objc func guidedDeletePopupChanged(_ sender: NSPopUpButton) {
        let isCreateNew = (sender.selectedItem?.representedObject as? String) == Self.createNewMarker
        guidedDeleteNewNameField.isHidden = !isCreateNew
        guidedDeleteConfirmButton.title = isCreateNew ? "Create, Switch & Delete" : "Switch & Delete"
        guidedDeleteErrorLabel.stringValue = ""
        updateGuidedDeleteConfirmEnabled()
    }

    /// Called on every keystroke in `guidedDeleteNewNameField` too (see
    /// controlTextDidChange in +State.swift) — the "immediate feedback" the
    /// design spec asks for.
    func updateGuidedDeleteConfirmEnabled() {
        guard let popup = guidedDeletePopup, let confirm = guidedDeleteConfirmButton else { return }
        let isCreateNew = guidedDeleteForcedCreate || (popup.selectedItem?.representedObject as? String) == Self.createNewMarker
        confirm.isEnabled = isCreateNew
            ? Self.isValidGcloudConfigName(guidedDeleteNewNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines))
            : popup.selectedItem != nil
    }

    @objc func guidedDeleteCancel() {
        guard let sheet = guidedDeleteWindow, let parent = window else { return }
        parent.endSheet(sheet)
        setStatus("Delete cancelled")
    }

    @objc func guidedDeleteConfirm() {
        guard let popup = guidedDeletePopup else { return }
        let isCreateNew = guidedDeleteForcedCreate || (popup.selectedItem?.representedObject as? String) == Self.createNewMarker
        let newName = guidedDeleteNewNameField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        let chosen = isCreateNew ? newName : (popup.titleOfSelectedItem ?? "")
        guard !chosen.isEmpty else { return }
        if isCreateNew, !Self.isValidGcloudConfigName(chosen) {
            guidedDeleteErrorLabel.stringValue = "Configuration names must start with a lowercase letter and contain only lowercase letters, digits, and hyphens."
            return
        }

        guidedDeleteErrorLabel.stringValue = ""
        guidedDeleteConfirmButton.isEnabled = false
        guidedDeleteCancelButton.isEnabled = false
        guidedDeleteSpinner.startAnimation(nil)

        let targetName = guidedDeleteTargetName
        let env = profile.envVars.asDictionary()
        service.runAsync({
            if isCreateNew {
                try self.service.save(provider: "gcp", chosen, fields: [:], extraEnv: env)
            }
            try self.service.activate(provider: "gcp", chosen, extraEnv: env)
            try self.service.delete(provider: "gcp", targetName, extraEnv: env)
        }) { [weak self] result in
            guard let self else { return }
            self.guidedDeleteSpinner.stopAnimation(nil)
            self.guidedDeleteConfirmButton.isEnabled = true
            self.guidedDeleteCancelButton.isEnabled = true
            switch result {
            case .success:
                if let sheet = self.guidedDeleteWindow, let parent = self.window {
                    parent.endSheet(sheet)
                }
                self.selectedProfileName = nil
                self.refreshProfiles()
                self.clearDetailForNoSelection()
                self.setStatus("Activated \(chosen), then deleted \(targetName)")
            case .failure(let error):
                // Do NOT close the sheet — render the error inline so the
                // user can retry, per the guided-errors principle (never a
                // dead end).
                self.guidedDeleteErrorLabel.stringValue = error.localizedDescription
            }
        }
    }

    /// Mirrors gcloud's own configuration naming rule (internal/gcpcreds'
    /// `nameRe`: `^[a-z][-a-z0-9]*$`) client-side, for immediate feedback —
    /// the Go side still re-validates on Save, this is not the only guard.
    private static func isValidGcloudConfigName(_ name: String) -> Bool {
        guard let first = name.first, ("a"..."z").contains(first) else { return false }
        return name.allSatisfy { ("a"..."z").contains($0) || ("0"..."9").contains($0) || $0 == "-" }
    }
}
