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
            canReviewConflict: { [weak self] provider, name in
                guard let self else { return false }
                return self.profileExists(name, provider: provider)
            },
            onReviewConflict: { [weak self] provider, name in
                guard let self else { return }
                if self.profile.allowsConnection(provider: provider, account: name) {
                    self.refreshProfiles(selecting: name, provider: provider)
                } else {
                    // The conflicting credentials profile exists on this Mac
                    // but is not visible in the fail-closed Workspace. Let the
                    // user grant/review it explicitly before editing or
                    // deleting it; never bypass the Workspace policy merely
                    // to make the conflict button appear to work.
                    self.openScopeSheet()
                }
            },
            onApplied: { [weak self] provider, names, addToWorkspace in
                guard let self else { return nil }
                defer { self.refreshAllWorkspacePolicies() }
                if addToWorkspace {
                    do {
                        var latest = self.profile
                        for name in names {
                            latest = try self.service.addConnectionToWorkspace(
                                profileID: self.profile.id,
                                provider: provider,
                                account: name
                            )
                        }
                        self.profile = latest
                        NotificationCenter.default.post(name: .profileDidChange, object: latest.id)
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
                } else if let latest = try? self.service.refreshProfile(id: self.profile.id) {
                    // Core may have removed a stale grant before recreating a
                    // same-named GCP configuration. Always render the accepted
                    // post-apply policy, even when the user did not grant the
                    // imported Connection to this Workspace.
                    self.profile = latest
                    NotificationCenter.default.post(name: .profileDidChange, object: latest.id)
                }
                self.refreshProfiles(selecting: names.first, provider: provider)
                self.setStatus("Connection sync complete")
                return nil
            },
            onApplyFailure: { [weak self] in
                guard let self else { return }
                if let latest = try? self.service.refreshProfile(id: self.profile.id) {
                    self.profile = latest
                    NotificationCenter.default.post(name: .profileDidChange, object: latest.id)
                }
                self.refreshProfiles()
                self.refreshAllWorkspacePolicies()
            },
            onDismiss: { [weak self] in
                self?.connectionSyncController = nil
            }
        )
        connectionSyncController = controller
        controller.present(on: parent)
    }
}
