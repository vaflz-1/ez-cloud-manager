import AppKit

extension CloudAccountsWindowController {
    @objc func openConnectionSyncSheet() {
        guard let parent = window else { return }
        if let existing = connectionSyncController, let sheet = existing.window {
            if sheet.sheetParent != nil {
                sheet.makeKey()
            } else {
                existing.present(on: parent)
            }
            return
        }

        let requested = currentEditingProvider()
        let initialProvider = ["aws", "gcp"].contains(requested) ? requested : "aws"
        let supported = catalog.providers.filter(\.supportsConnectionAuth)
        guard !supported.isEmpty else {
            showError("This build has no connector that supports browser sign-in.")
            return
        }

        let controller = ConnectionAuthSyncSheetController(
            providerID: initialProvider,
            providers: supported,
            workspaceName: profile.name,
            workspaceIsScoped: !profile.cloudAccountsSettings.showAllAccounts,
            workspaceConnections: Set(profile.cloudAccountsSettings.accounts),
            extraEnv: profile.envVars.asDictionary(),
            service: service,
            conflictsWithOpenDraft: { [weak self] provider, name in
                guard let self else { return false }
                self.commitActiveEdits()
                let editorName = self.profileNameField.stringValue
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                return self.currentEditingProvider() == provider
                    && editorName == name
                    && self.hasUnsavedConnectionChanges()
            },
            onApplied: { [weak self] provider, names, addToWorkspace in
                guard let self else { return nil }
                if addToWorkspace {
                    var settings = self.profile.cloudAccountsSettings
                    var refs = Set(settings.accounts)
                    names.forEach { refs.insert(AccountRef(provider: provider, account: $0)) }
                    settings.accounts = Array(refs).sorted {
                        $0.provider == $1.provider
                            ? $0.account.localizedCaseInsensitiveCompare($1.account) == .orderedAscending
                            : $0.provider < $1.provider
                    }
                    do {
                        let saved = try self.service.saveCloudAccountsSettings(
                            profileID: self.profile.id,
                            settings
                        )
                        self.profile = saved
                        NotificationCenter.default.post(name: .profileDidChange, object: saved.id)
                    } catch {
                        // The connection apply already succeeded; report the
                        // narrower scope failure honestly and keep every
                        // imported connection discoverable through Show All.
                        self.showError(
                            "Connections were synced, but the workspace scope could not be updated: \(error.localizedDescription)"
                        )
                        self.refreshProfiles(selecting: names.first, provider: provider)
                        self.setStatus("Connections synced · workspace visibility update failed")
                        return "the workspace visibility setting could not be updated"
                    }
                }
                self.refreshProfiles(selecting: names.first, provider: provider)
                self.setStatus("Connection sync complete")
                return nil
            },
            onApplyFailure: { [weak self] in
                self?.refreshProfiles()
            },
            onDismiss: { [weak self] in
                self?.connectionSyncController = nil
            }
        )
        connectionSyncController = controller
        controller.present(on: parent)
    }
}
