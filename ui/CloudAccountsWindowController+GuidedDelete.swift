import AppKit

private struct GuidedDeleteOutcome {
    let createdReplacement: Bool
    let activatedReplacement: Bool
    let deletedTarget: Bool
    let scopedProfile: Profile?
    let scopeFailure: String?
    let operationFailure: String?
}

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
        // A provider configuration can exist on the machine without being
        // authorized for this Workspace. Never offer it as a replacement: the
        // guided flow must not become a scope bypass.
        let others = (profilesByProvider["gcp"] ?? [])
            .map(\.name)
            .filter { $0 != name && profile.allowsConnection(provider: "gcp", account: $0) }
        guidedDeleteForcedCreate = others.isEmpty

        let sheet = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 460, height: 270),
            styleMask: [.titled],
            backing: .buffered, defer: false)
        guidedDeleteWindow = sheet

        let content = NSView()
        sheet.contentView = content

        let titleLabel = NSTextField(wrappingLabelWithString: "“\(name)” is the active gcloud configuration")
        titleLabel.font = .systemFont(ofSize: 13, weight: .semibold)
        titleLabel.translatesAutoresizingMaskIntoConstraints = false

        let bodyText = guidedDeleteForcedCreate
            ? "This is the only replacement this Workspace may use. Create another configuration first. Kervik will make it active for every terminal and app using gcloud on this Mac, then delete “\(name)”."
            : "Pick another configuration to make active for every terminal and app using gcloud on this Mac. Kervik will then delete “\(name)”."
        let bodyLabel = NSTextField(wrappingLabelWithString: bodyText)
        bodyLabel.font = .systemFont(ofSize: 11)
        bodyLabel.textColor = .secondaryLabelColor
        bodyLabel.translatesAutoresizingMaskIntoConstraints = false

        let popupCaption = NSTextField(labelWithString: "Make active on this Mac:")
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
        UI.style(cancelButton, as: .secondary)
        cancelButton.translatesAutoresizingMaskIntoConstraints = false
        guidedDeleteCancelButton = cancelButton

        let confirmButton = NSButton(
            title: guidedDeleteForcedCreate ? "Create, Activate & Delete" : "Activate & Delete",
            target: self, action: #selector(guidedDeleteConfirm))
        UI.style(confirmButton, as: .primary)
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
        guidedDeleteConfirmButton.title = isCreateNew ? "Create, Activate & Delete" : "Activate & Delete"
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
        guard requireCurrentConnectionAuthorization(provider: "gcp", name: guidedDeleteTargetName) else {
            if let sheet = guidedDeleteWindow, let parent = window {
                parent.endSheet(sheet)
            }
            return
        }
        if !isCreateNew,
           !requireCurrentConnectionAuthorization(provider: "gcp", name: chosen) {
            return
        }
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
        let workspaceProfileID = profile.id
        service.runAsync({
            var createdReplacement = false
            var activatedReplacement = false
            var scopedProfile: Profile?

            if isCreateNew {
                do {
                    try self.service.save(
                        provider: "gcp",
                        chosen,
                        workspaceID: workspaceProfileID,
                        fields: [:],
                        expectAbsent: true,
                        extraEnv: env
                    )
                    createdReplacement = true
                } catch {
                    return GuidedDeleteOutcome(
                        createdReplacement: false,
                        activatedReplacement: false,
                        deletedTarget: false,
                        scopedProfile: nil,
                        scopeFailure: nil,
                        operationFailure: error.localizedDescription
                    )
                }

                // A new replacement in a scoped Workspace must be authorized
                // before it is made machine-active or the old configuration is
                // deleted. If this write fails, stop after creation and report
                // that exact partial result.
                do {
                    let saved = try self.service.addConnectionToWorkspace(
                        profileID: workspaceProfileID,
                        provider: "gcp",
                        account: chosen
                    )
                    scopedProfile = saved
                } catch {
                    return GuidedDeleteOutcome(
                        createdReplacement: true,
                        activatedReplacement: false,
                        deletedTarget: false,
                        scopedProfile: nil,
                        scopeFailure: error.localizedDescription,
                        operationFailure: nil
                    )
                }
            }

            // The scope may change while save/auth CLI processes are running.
            // Re-check from the core immediately before the machine-global
            // activation and destructive delete stages.
            do {
                guard try self.service.isConnectionAllowed(
                    profileID: workspaceProfileID,
                    provider: "gcp",
                    account: chosen
                ) else {
                    return GuidedDeleteOutcome(
                        createdReplacement: createdReplacement,
                        activatedReplacement: false,
                        deletedTarget: false,
                        scopedProfile: scopedProfile,
                        scopeFailure: nil,
                        operationFailure: "Workspace access to “\(chosen)” was removed before activation."
                    )
                }
            } catch {
                return GuidedDeleteOutcome(
                    createdReplacement: createdReplacement,
                    activatedReplacement: false,
                    deletedTarget: false,
                    scopedProfile: scopedProfile,
                    scopeFailure: nil,
                    operationFailure: "Could not revalidate Workspace access: \(error.localizedDescription)"
                )
            }

            do {
                try self.service.activate(
                    provider: "gcp",
                    chosen,
                    workspaceID: workspaceProfileID,
                    extraEnv: env
                )
                activatedReplacement = true
            } catch {
                return GuidedDeleteOutcome(
                    createdReplacement: createdReplacement,
                    activatedReplacement: false,
                    deletedTarget: false,
                    scopedProfile: scopedProfile,
                    scopeFailure: nil,
                    operationFailure: error.localizedDescription
                )
            }

            do {
                guard try self.service.isConnectionAllowed(
                    profileID: workspaceProfileID,
                    provider: "gcp",
                    account: targetName
                ) else {
                    return GuidedDeleteOutcome(
                        createdReplacement: createdReplacement,
                        activatedReplacement: activatedReplacement,
                        deletedTarget: false,
                        scopedProfile: scopedProfile,
                        scopeFailure: nil,
                        operationFailure: "Workspace access to “\(targetName)” was removed before deletion."
                    )
                }
            } catch {
                return GuidedDeleteOutcome(
                    createdReplacement: createdReplacement,
                    activatedReplacement: activatedReplacement,
                    deletedTarget: false,
                    scopedProfile: scopedProfile,
                    scopeFailure: nil,
                    operationFailure: "Could not revalidate Workspace access before deletion: \(error.localizedDescription)"
                )
            }

            do {
                try self.service.delete(
                    provider: "gcp",
                    targetName,
                    workspaceID: workspaceProfileID,
                    extraEnv: env
                )
            } catch CredentialsService.ServiceError.connectionDeletedScopeCleanupFailed(let message) {
                return GuidedDeleteOutcome(
                    createdReplacement: createdReplacement,
                    activatedReplacement: activatedReplacement,
                    deletedTarget: true,
                    scopedProfile: scopedProfile,
                    scopeFailure: message,
                    operationFailure: nil
                )
            } catch {
                return GuidedDeleteOutcome(
                    createdReplacement: createdReplacement,
                    activatedReplacement: activatedReplacement,
                    deletedTarget: false,
                    scopedProfile: scopedProfile,
                    scopeFailure: nil,
                    operationFailure: error.localizedDescription
                )
            }

            // The target is gone from the provider store. Remove its stale
            // Workspace reference without undoing a newly-added replacement.
            do {
                scopedProfile = try self.service.removeConnectionFromWorkspace(
                    profileID: workspaceProfileID,
                    provider: "gcp",
                    account: targetName
                )
            } catch {
                return GuidedDeleteOutcome(
                    createdReplacement: createdReplacement,
                    activatedReplacement: true,
                    deletedTarget: true,
                    scopedProfile: scopedProfile,
                    scopeFailure: error.localizedDescription,
                    operationFailure: nil
                )
            }

            return GuidedDeleteOutcome(
                createdReplacement: createdReplacement,
                activatedReplacement: true,
                deletedTarget: true,
                scopedProfile: scopedProfile,
                scopeFailure: nil,
                operationFailure: nil
            )
        }) { [weak self] result in
            guard let self else { return }
            self.guidedDeleteSpinner.stopAnimation(nil)
            self.guidedDeleteConfirmButton.isEnabled = true
            self.guidedDeleteCancelButton.isEnabled = true
            switch result {
            case .success(let outcome):
                if outcome.createdReplacement || outcome.deletedTarget {
                    self.refreshAllWorkspacePolicies()
                }
                if let scopedProfile = outcome.scopedProfile {
                    self.profile = scopedProfile
                    NotificationCenter.default.post(name: .profileDidChange, object: scopedProfile.id)
                }

                if outcome.deletedTarget {
                    if let sheet = self.guidedDeleteWindow, let parent = self.window {
                        parent.endSheet(sheet)
                    }
                    self.selectedProfileName = nil
                    self.refreshProfiles()
                    self.clearDetailForNoSelection()
                    if let scopeFailure = outcome.scopeFailure {
                        self.setStatus("Activated \(chosen) and deleted \(targetName) · workspace cleanup failed")
                        self.presentScopeUpdateWarning(
                            completedAction: "Activated \(chosen) and deleted \(targetName)",
                            error: scopeFailure
                        )
                    } else {
                        self.setStatus("Activated \(chosen) on this Mac, then deleted \(targetName)")
                    }
                    return
                }

                if outcome.createdReplacement {
                    // The create is not safely retryable with expectAbsent.
                    // Close this attempt and refresh so the new real state is
                    // offered as an existing replacement on the next attempt.
                    if let sheet = self.guidedDeleteWindow, let parent = self.window {
                        parent.endSheet(sheet)
                    }
                    self.refreshProfiles(selecting: chosen, provider: "gcp")
                    if let scopeFailure = outcome.scopeFailure {
                        self.setStatus("Created \(chosen) · target not deleted · workspace update failed")
                        self.presentScopeUpdateWarning(
                            completedAction: "Created \(chosen); \(targetName) was not deleted",
                            error: scopeFailure
                        )
                    } else {
                        let stage = outcome.activatedReplacement
                            ? "Created and activated \(chosen), but did not delete \(targetName)"
                            : "Created \(chosen), but did not activate it or delete \(targetName)"
                        self.setStatus(stage)
                        self.presentGuidedDeletePartialWarning(
                            completedAction: stage,
                            error: outcome.operationFailure ?? "The remaining operation did not complete."
                        )
                    }
                    return
                }

                // No mutation happened, or an existing alternative was made
                // active but the target delete failed. Keep the sheet open so
                // the user can retry, while naming any global state change.
                if outcome.activatedReplacement {
                    self.guidedDeleteErrorLabel.stringValue = "“\(chosen)” is now active on this Mac, but “\(targetName)” was not deleted: \(outcome.operationFailure ?? "Unknown error")"
                } else {
                    self.guidedDeleteErrorLabel.stringValue = outcome.operationFailure ?? "The operation did not complete."
                }
            case .failure(let error):
                // Do NOT close the sheet — render the error inline so the
                // user can retry, per the guided-errors principle (never a
                // dead end).
                self.guidedDeleteErrorLabel.stringValue = error.localizedDescription
            }
        }
    }

    private func presentGuidedDeletePartialWarning(completedAction: String, error: String) {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = completedAction
        alert.informativeText = error
        alert.addButton(withTitle: "OK")
        alert.runModal()
    }

    /// Mirrors gcloud's own configuration naming rule (internal/gcpcreds'
    /// `nameRe`: `^[a-z][-a-z0-9]*$`) client-side, for immediate feedback —
    /// the Go side still re-validates on Save, this is not the only guard.
    private static func isValidGcloudConfigName(_ name: String) -> Bool {
        guard let first = name.first, ("a"..."z").contains(first) else { return false }
        return name.allSatisfy { ("a"..."z").contains($0) || ("0"..."9").contains($0) || $0 == "-" }
    }
}
