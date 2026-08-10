import AppKit

/// The **Transfer** built-in plugin (internal/plugin.TransferID) —
/// whole-PROFILE `.ezprofile` export/import (docs/PLATFORM.md's
/// "profiles-import" migration-map entry), scoped to exactly the profile
/// that owns this Hub window. This is NOT the credential-entry-level
/// export/compare in CloudAccountsWindowController+Transfer.swift — that
/// operates on "whichever credential entry is selected in Cloud Accounts'
/// own sidebar" and stays there, so this plugin never couples back to
/// another plugin's selection state. No picker, no ambiguity: export writes
/// THIS profile; import always creates a brand-new, DIFFERENT profile.
final class TransferWindowController: NSWindowController {
    private let service: CredentialsService
    private var profile: Profile

    private var explainerLabel: NSTextField!
    private var exportButton: NSButton!
    private var statusLabel: NSTextField!

    init(profile: Profile, service: CredentialsService) {
        self.profile = profile
        self.service = service
        super.init(window: nil)
        buildWindow()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func show() {
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func updateProfile(_ profile: Profile) {
        self.profile = profile
        renderProfile()
    }

    private func buildWindow() {
        let win = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 420, height: 220),
            styleMask: [.titled, .closable, .miniaturizable],
            backing: .buffered, defer: false)
        win.center()
        self.window = win

        let content = NSView()
        win.contentView = content

        explainerLabel = NSTextField(wrappingLabelWithString: "")
        explainerLabel.font = .systemFont(ofSize: 12)
        explainerLabel.textColor = .secondaryLabelColor
        explainerLabel.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(explainerLabel)

        exportButton = NSButton(title: "", target: self, action: #selector(exportTapped))
        exportButton.bezelStyle = .rounded
        exportButton.controlSize = .large
        exportButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(exportButton)

        let importButton = NSButton(title: "Import .ezprofile…", target: self, action: #selector(importTapped))
        importButton.bezelStyle = .rounded
        importButton.controlSize = .large
        importButton.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(importButton)

        statusLabel = NSTextField(labelWithString: "Ready")
        statusLabel.font = .systemFont(ofSize: 11)
        statusLabel.textColor = .secondaryLabelColor
        statusLabel.lineBreakMode = .byTruncatingTail
        statusLabel.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(statusLabel)

        NSLayoutConstraint.activate([
            explainerLabel.topAnchor.constraint(equalTo: content.topAnchor, constant: 20),
            explainerLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            explainerLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),

            exportButton.topAnchor.constraint(equalTo: explainerLabel.bottomAnchor, constant: 24),
            exportButton.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            exportButton.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),

            importButton.topAnchor.constraint(equalTo: exportButton.bottomAnchor, constant: 10),
            importButton.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            importButton.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),

            statusLabel.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 20),
            statusLabel.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -20),
            statusLabel.bottomAnchor.constraint(equalTo: content.bottomAnchor, constant: -20)
        ])
        renderProfile()
    }

    private func renderProfile() {
        window?.title = Product.toolTitle("Transfer", workspace: profile.name)
        explainerLabel?.stringValue = "Export “\(profile.name)” as a .ezprofile file for another machine, or import one as a new workspace."
        exportButton?.title = "Export “\(profile.name)”…"
    }

    @objc private func exportTapped() {
        let panel = NSSavePanel()
        panel.title = "Export \(profile.name)"
        panel.nameFieldStringValue = "\(profile.name).ezprofile"
        panel.canCreateDirectories = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            try service.exportProfile(id: profile.id, to: url)
            setStatus("Exported → \(url.lastPathComponent)")
        } catch {
            showError(error.localizedDescription)
        }
    }

    @objc private func importTapped() {
        let panel = NSOpenPanel()
        panel.title = "Import a .ezprofile file"
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            let imported = try service.importProfile(from: url)
            // Deliberately does NOT touch self.profile or this window's
            // title — import always creates a DIFFERENT profile (fresh id),
            // never overwrites the one this Transfer window is scoped to.
            setStatus("Imported as “\(imported.name)” — use File ▸ New Window with Workspace to open it")
        } catch {
            showError(error.localizedDescription)
        }
    }

    private func setStatus(_ message: String) {
        statusLabel.stringValue = message
    }

    private func showError(_ message: String) {
        setStatus("Error")
        let alert = NSAlert()
        alert.messageText = "Transfer"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }
}
