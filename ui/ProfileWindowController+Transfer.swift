import AppKit
import UniformTypeIdentifiers

/// Export / import / compare / activate — moving profile data in and out of
/// the app. Secret hygiene rules live here: clipboard exports are concealed,
/// file exports go only where the user explicitly points, imports never
/// auto-save.
extension ProfileWindowController {
    static let exportFormats = ["env", "dotenv", "ini", "json"]

    private func exportableSelection() -> (provider: String, name: String)? {
        guard let name = selectedProfileName else {
            showError("Select a saved profile to export.")
            return nil
        }
        return (selectedProvider, name)
    }

    /// Copies the saved profile in the chosen format to the clipboard,
    /// flagged concealed. Exports read what is on disk — unsaved edits are
    /// not included, which the status line makes explicit.
    @objc func exportTapped(_ sender: NSMenuItem) {
        guard let target = exportableSelection() else { return }
        guard sender.tag >= 0, sender.tag < Self.exportFormats.count else { return }
        let format = Self.exportFormats[sender.tag]
        do {
            let text = try service.export(provider: target.provider, target.name, format: format, extraEnv: profile.envVars.asDictionary())
            guard !text.isEmpty else {
                setStatus("Nothing to export — profile has no saved values")
                return
            }
            copyConcealed(text)
            setStatus("Copied \(target.name) as \(format) (saved values, concealed from clipboard history)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc func exportToFile() {
        guard let target = exportableSelection() else { return }

        let panel = NSSavePanel()
        panel.title = "Export \(target.name)"
        panel.nameFieldStringValue = "\(target.name).env"
        panel.canCreateDirectories = true

        let formatPopup = NSPopUpButton()
        for format in Self.exportFormats {
            formatPopup.addItem(withTitle: format)
        }
        let label = NSTextField(labelWithString: "Format:")
        label.font = .systemFont(ofSize: 11)
        let stack = NSStackView(views: [label, formatPopup])
        stack.orientation = .horizontal
        stack.spacing = 6
        panel.accessoryView = stack

        guard panel.runModal() == .OK, let url = panel.url else { return }
        let format = Self.exportFormats[max(0, formatPopup.indexOfSelectedItem)]
        do {
            let text = try service.export(provider: target.provider, target.name, format: format, extraEnv: profile.envVars.asDictionary())
            // 0600: an exported profile is as sensitive as the store itself.
            try text.data(using: .utf8)?.write(to: url, options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
            setStatus("Exported \(target.name) → \(url.lastPathComponent) (\(format), 0600)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    /// Reads a config/credentials/JSON file into the paste box and parses it
    /// with the current provider's parser — same trust path as pasting.
    @objc func importFromFile() {
        let panel = NSOpenPanel()
        panel.title = "Import credentials or config"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.showsHiddenFiles = true

        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            let raw = try Data(contentsOf: url)
            guard raw.count <= 1 << 20 else {
                showError("File is larger than 1 MB — not a credentials/config file.")
                return
            }
            guard let text = String(data: raw, encoding: .utf8) else {
                showError("File is not UTF-8 text.")
                return
            }
            pasteView.string = text
            if !applyParsedCredentialsFromPaste(force: true, userInitiated: true) {
                showError("No \(catalog.providerDisplayName(currentEditingProvider())) variables recognized in \(url.lastPathComponent).")
            }
        } catch {
            showError(error.localizedDescription)
        }
    }

    /// Side-by-side compare: pick another profile of the same provider and
    /// see the field diff (secrets masked) without touching anything.
    @objc func compareProfiles() {
        guard let name = selectedProfileName else {
            showError("Select a profile to compare.")
            return
        }
        let provider = selectedProvider
        let others = (profilesByProvider[provider] ?? []).map(\.name).filter { $0 != name }
        guard !others.isEmpty else {
            showError("No other \(catalog.providerDisplayName(provider)) profiles to compare with.")
            return
        }

        let picker = NSAlert()
        picker.messageText = "Compare “\(name)” with…"
        picker.informativeText = "Values are read from disk; secrets stay masked."
        let popup = NSPopUpButton(frame: NSRect(x: 0, y: 0, width: 260, height: 26))
        popup.addItems(withTitles: others)
        picker.accessoryView = popup
        picker.addButton(withTitle: "Compare")
        picker.addButton(withTitle: "Cancel")
        guard picker.runModal() == .alertFirstButtonReturn, let otherName = popup.selectedItem?.title else { return }

        do {
            let extraEnv = profile.envVars.asDictionary()
            let mine = try service.get(provider: provider, name, extraEnv: extraEnv).fields
            let theirs = try service.get(provider: provider, otherName, extraEnv: extraEnv).fields
            let diff = diffGroups(old: mine, new: theirs)
            let total = diff.added.count + diff.changed.count + diff.removed.count

            let result = NSAlert()
            result.messageText = "\(name)  →  \(otherName)"
            result.informativeText = total == 0
                ? "The profiles are identical."
                : "\(total) difference\(total == 1 ? "" : "s"). “Added” exists only in \(otherName); “removed” only in \(name)."
            if total > 0 {
                result.accessoryView = diffAccessoryView(diff)
            }
            result.addButton(withTitle: "Done")
            result.runModal()
        } catch {
            showError(error.localizedDescription)
        }
    }

    /// Provider-native "make this the active/default profile", with an
    /// explicit explanation of what will actually happen.
    @objc func activateProfile() {
        guard let name = selectedProfileName, let info = catalog.providerInfo(selectedProvider), info.canActivate else { return }

        let alert = NSAlert()
        alert.messageText = info.activateLabel ?? "Set active profile"
        switch selectedProvider {
        case "aws":
            alert.informativeText = "The fields of “\(name)” will be copied over the [default] profile in \(pathsByProvider["aws"] ?? "~/.aws/credentials"). “\(name)” itself is not modified; a timestamped backup is written first."
        case "gcp":
            alert.informativeText = "gcloud will switch to configuration “\(name)” (same as `gcloud config configurations activate \(name)`)."
        default:
            alert.informativeText = "Profile “\(name)” becomes the active one."
        }
        alert.addButton(withTitle: "Proceed")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }

        do {
            try service.activate(provider: selectedProvider, name, extraEnv: profile.envVars.asDictionary())
            refreshProfiles()
            setStatus("\(name) is now active (\(catalog.providerDisplayName(selectedProvider)))")
        } catch {
            showError(error.localizedDescription)
        }
    }

    // MARK: - Ko-fi & Launch Templates entry points

    @objc func openKoFi() {
        NSWorkspace.shared.open(AppDelegate.koFiURL)
    }

    @objc func openLaunchTemplates() {
        guard selectedProvider == "aws", let profileName = selectedProfileName else {
            showError("Select an AWS profile first — Launch Templates are read with its credentials.")
            return
        }
        let region = fieldRows.first { $0.key == "region" }?.value.trimmingCharacters(in: .whitespacesAndNewlines)
        presentLaunchTemplates(profile: profileName, region: region)
    }

    /// A tool row in the sidebar was activated. Tools open in their own
    /// surface; the sidebar selection returns to the profile afterwards so
    /// the detail pane and the list never disagree.
    func openTool(provider: String, toolID: String) {
        defer { restoreSidebarSelection() }
        switch (provider, toolID) {
        case ("aws", "launch-templates"):
            // Prefer the profile the user is looking at; else the first AWS
            // profile — the tool needs *some* credentials to call AWS with.
            if selectedProvider == "aws", let name = selectedProfileName {
                let region = fieldRows.first { $0.key == "region" }?.value.trimmingCharacters(in: .whitespacesAndNewlines)
                presentLaunchTemplates(profile: name, region: region)
            } else if let first = (profilesByProvider["aws"] ?? []).first?.name {
                let region = (try? service.get(provider: "aws", first, extraEnv: self.profile.envVars.asDictionary()))?.fields["region"]
                presentLaunchTemplates(profile: first, region: region)
            } else {
                showError("Add an AWS profile first — Launch Templates are read with its credentials.")
            }
        default:
            break
        }
    }

    /// Retains this window's own Launch Templates window — one per profile
    /// window, so two windows never fight over one AWS-account-bound session.
    private func presentLaunchTemplates(profile profileName: String, region: String?) {
        let controller = launchTemplatesController ?? LaunchTemplatesWindowController(service: service)
        launchTemplatesController = controller
        let resolved = (region?.isEmpty == false) ? region! : "us-east-1"
        controller.present(profile: profileName, region: resolved)
    }

    /// Puts the sidebar selection back on the loaded profile (or clears it)
    /// without re-triggering selection side effects.
    private func restoreSidebarSelection() {
        isRebuildingSidebar = true
        defer { isRebuildingSidebar = false }
        if let name = selectedProfileName, let idx = sidebarIndex(ofProfile: name, provider: selectedProvider) {
            profilesTable.selectRowIndexes(IndexSet(integer: idx), byExtendingSelection: false)
        } else {
            profilesTable.deselectAll(nil)
        }
    }
}
