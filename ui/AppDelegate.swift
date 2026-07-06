import AppKit
import Foundation

/// AppDelegate owns only app-global concerns: the shared CredentialsService
/// and ProviderCatalog, the registry of currently-open profile windows, and
/// the Profile Manager singleton window. Everything that used to live
/// directly on AppDelegate before the platform-v2.0 multi-window split
/// (sidebar, detail editor, Launch Templates, …) now lives on
/// ProfileWindowController instead — see its own doc comment for the module
/// breakdown.
final class AppDelegate: NSObject, NSApplicationDelegate {
    /// Boundary to the `ezcloud` CLI. Stateless aside from toolPath — safe
    /// to share across every window.
    let service = CredentialsService()
    /// Registered providers + schemas, loaded once and shared read-only.
    let catalog = ProviderCatalog()

    /// Open profile windows, keyed by profile id. At most one window per
    /// profile — reopening a profile that's already open refocuses it
    /// instead of creating a second, divergent edit surface.
    var windowControllers: [String: ProfileWindowController] = [:]
    var profileManagerController: ProfileManagerWindowController?

    static let koFiURL = URL(string: "https://ko-fi.com/vaflz")!

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        configureMainMenu()

        do {
            try service.ensureProfilesMigrated()
        } catch {
            // Migration failure must not block launch — worst case the app
            // runs against whatever profile.json files already exist.
            NSLog("profile migration failed: \(error.localizedDescription)")
        }
        do {
            try catalog.load(using: service)
        } catch {
            NSLog("loading providers failed: \(error.localizedDescription)")
        }

        NotificationCenter.default.addObserver(forName: .openProfileWindowRequested, object: nil, queue: .main) { [weak self] note in
            guard let id = note.object as? String else { return }
            self?.openWindow(forProfile: id)
        }
        NotificationCenter.default.addObserver(forName: .profileWindowWillClose, object: nil, queue: .main) { [weak self] note in
            guard let controller = note.object as? ProfileWindowController else { return }
            self?.windowControllers = self?.windowControllers.filter { $0.value !== controller } ?? [:]
        }

        openMostRecentOrDefaultProfile()
        NSApp.activate(ignoringOtherApps: true)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    /// P0 opens exactly one window on launch — the most-recently-updated
    /// profile — rather than restoring every previously-open window. That is
    /// an explicit scope cut (docs/PLATFORM.md phase P0), not a missed
    /// requirement; migration guarantees at least one profile always exists.
    private func openMostRecentOrDefaultProfile() {
        guard let profiles = try? service.listProfiles(), !profiles.isEmpty else {
            showError("No profile could be loaded or created — check that the ezcloud CLI is reachable.")
            return
        }
        let mostRecent = profiles.max { $0.updatedAt < $1.updatedAt } ?? profiles[0]
        openWindow(forProfile: mostRecent.id)
    }

    /// Opens a window bound to the given profile id, reusing (and
    /// refocusing) an already-open one rather than creating a duplicate.
    func openWindow(forProfile id: String) {
        if let existing = windowControllers[id] {
            existing.show()
            return
        }
        guard let profile = try? service.getProfile(id: id) else {
            showError("That profile could not be loaded.")
            return
        }
        let controller = ProfileWindowController(profile: profile, catalog: catalog, service: service)
        windowControllers[id] = controller
        controller.show()
    }

    @objc func newWindowForProfile(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        openWindow(forProfile: id)
    }

    @objc func manageProfiles() {
        let controller = profileManagerController ?? ProfileManagerWindowController(service: service)
        profileManagerController = controller
        controller.show()
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "EZ Cloud Manager"
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }
}
