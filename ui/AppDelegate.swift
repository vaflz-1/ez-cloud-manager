import AppKit
import Foundation

/// AppDelegate owns only app-global concerns: the shared CredentialsService
/// and ProviderCatalog, the registry of currently-open Plugin Hub windows,
/// and the Profile Manager singleton window. Everything that used to live
/// directly on AppDelegate before the platform-v2.0 multi-window split
/// (sidebar, detail editor, Launch Templates, …) — and everything the P1
/// plugin host (docs/PLATFORM.md) since split out of the Hub into its own
/// built-in plugin windows — lives elsewhere; see ProfileWindowController's
/// own doc comment for the module breakdown.
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
    private var workspaceRefreshGeneration = 0

    static let koFiURL = URL(string: "https://ko-fi.com/vaflz")!

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        applySavedAppearance()
        configureMainMenu()

        do {
            let bootstrap = try service.bootstrap()
            catalog.load(from: bootstrap)
            openWindow(
                for: bootstrap.activeProfile,
                preloadedProfiles: bootstrap.profiles,
                preloadedAddons: bootstrap.addons
            )
        } catch {
            // Compatibility fallback keeps the shell usable with an older
            // development CLI while production bundles take the one-call path.
            NSLog("app bootstrap failed, using compatibility path: \(error.localizedDescription)")
            do { try service.ensureProfilesMigrated() } catch {
                NSLog("profile migration failed: \(error.localizedDescription)")
            }
            do { try catalog.load(using: service) } catch {
                NSLog("loading providers failed: \(error.localizedDescription)")
            }
            openMostRecentOrDefaultProfile()
        }

        NotificationCenter.default.addObserver(forName: .openProfileWindowRequested, object: nil, queue: .main) { [weak self] note in
            guard let id = note.object as? String else { return }
            self?.openWindow(forProfile: id)
        }
        NotificationCenter.default.addObserver(forName: .profileWindowWillClose, object: nil, queue: .main) { [weak self] note in
            guard let controller = note.object as? ProfileWindowController else { return }
            self?.windowControllers = self?.windowControllers.filter { $0.value !== controller } ?? [:]
        }
        NotificationCenter.default.addObserver(forName: .manageProfilesRequested, object: nil, queue: .main) { [weak self] _ in
            self?.manageProfiles()
        }
        NotificationCenter.default.addObserver(forName: .profileSwitchRequested, object: nil, queue: .main) { [weak self] note in
            guard let request = note.object as? ProfileSwitchRequest else { return }
            self?.handleProfileSwitchRequest(request)
        }
        NotificationCenter.default.addObserver(forName: .profileListDidChange, object: nil, queue: .main) { [weak self] note in
            guard let change = note.object as? ProfileListChange else { return }
            self?.handleProfileListChange(change)
        }
        NotificationCenter.default.addObserver(forName: .refreshWorkspaceStateRequested, object: nil, queue: .main) { [weak self] _ in
            self?.refreshWorkspaceState(nil)
        }
        NotificationCenter.default.addObserver(forName: .profileDeletionPreflightRequested, object: nil, queue: .main) { [weak self] note in
            guard let request = note.object as? ProfileContextChangePreflight,
                  let controller = self?.windowControllers[request.profileID]
            else { return }
            request.allowed = controller.authorizeProfileContextExit()
        }
        NotificationCenter.default.addObserver(forName: .profileEnvironmentChangePreflightRequested, object: nil, queue: .main) { [weak self] note in
            guard let request = note.object as? ProfileContextChangePreflight,
                  let controller = self?.windowControllers[request.profileID]
            else { return }
            request.allowed = controller.authorizeProfileContextExit()
        }

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
            showError("No workspace could be loaded or created — check that the local core is reachable.")
            return
        }
        let mostRecent = profiles.max { lhs, rhs in
            let left = AppTimestamp.date(lhs.updatedAt) ?? .distantPast
            let right = AppTimestamp.date(rhs.updatedAt) ?? .distantPast
            return left < right
        } ?? profiles[0]
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
            showError("That workspace could not be loaded.")
            return
        }
        openWindow(for: profile)
    }

    private func openWindow(
        for profile: Profile,
        preloadedProfiles: [Profile]? = nil,
        preloadedAddons: [PluginDescriptor]? = nil
    ) {
        if let existing = windowControllers[profile.id] {
            existing.show()
            return
        }
        let controller = ProfileWindowController(
            profile: profile,
            catalog: catalog,
            service: service,
            preloadedProfiles: preloadedProfiles,
            preloadedAddons: preloadedAddons
        )
        windowControllers[profile.id] = controller
        controller.show()
    }

    @objc func newWindowForProfile(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        openWindow(forProfile: id)
    }

    /// The Hub's profile switcher asked to move to a different profile. If
    /// that profile ALREADY has its own open window, refocus it and snap the
    /// requesting window's popup back — two windows must never end up bound
    /// to the same profile (windowControllers stays keyed 1:1 by id). Only
    /// when the target has no window yet does the requesting window rebind
    /// to it in place (same NSWindow, new profile/env/plugins/title).
    private func handleProfileSwitchRequest(_ request: ProfileSwitchRequest) {
        if let existing = windowControllers[request.targetProfileID], existing !== request.from {
            existing.show()
            request.from.revertProfilePopupSelection()
            return
        }
        guard let newProfile = try? service.getProfile(id: request.targetProfileID) else {
            showError("That workspace could not be loaded.")
            request.from.revertProfilePopupSelection()
            return
        }
        guard request.from.rebind(to: newProfile) else {
            request.from.revertProfilePopupSelection()
            return
        }
        if let oldID = windowControllers.first(where: { $0.value === request.from })?.key {
            windowControllers.removeValue(forKey: oldID)
        }
        windowControllers[request.targetProfileID] = request.from
    }

    /// A deleted profile must not leave a live Hub or addon session behind.
    /// Removing the exact dictionary entry before closing makes the
    /// `.profileWindowWillClose` callback harmlessly idempotent.
    private func handleProfileListChange(_ change: ProfileListChange) {
        guard change.mutation == .deleted,
              let controller = windowControllers.removeValue(forKey: change.profileID)
        else { return }
        controller.closeAfterProfileDeletion()
    }

    @objc func manageProfiles() {
        let controller = profileManagerController ?? ProfileManagerWindowController(service: service)
        profileManagerController = controller
        controller.show()
    }

    /// Reconciles changes made through the headless CLI in one process, then
    /// fans the resulting immutable snapshot out in memory. Repeated requests
    /// are generation-gated so an older completion can never roll state back.
    @objc func refreshWorkspaceState(_ sender: Any?) {
        startWorkspaceRefresh(retryIfSuperseded: true)
    }

    private func startWorkspaceRefresh(retryIfSuperseded: Bool) {
        workspaceRefreshGeneration += 1
        let generation = workspaceRefreshGeneration
        service.runAsync({
            let revision = self.service.beginCompleteProfileRead()
            return (revision, try self.service.readProfilesFromDisk())
        }) { [weak self] result in
            guard let self, generation == self.workspaceRefreshGeneration else { return }
            switch result {
            case .success(let (revision, profiles)):
                guard self.service.isCompleteProfileReadCurrent(revision) else {
                    if retryIfSuperseded {
                        self.startWorkspaceRefresh(retryIfSuperseded: false)
                    }
                    return
                }
                let byID = Dictionary(uniqueKeysWithValues: profiles.map { ($0.id, $0) })
                for (id, controller) in self.windowControllers {
                    if !controller.canApplyRefreshedSnapshot(byID[id]) {
                        return // keep the prior coherent snapshot and the local draft
                    }
                }
                guard let accepted = self.service.commitCompleteProfiles(
                    profiles,
                    ifUnchangedSince: revision
                ) else {
                    // A mutation completed during a draft-confirmation modal.
                    // The authorization was side-effect-free, so keep the
                    // draft protected and let that mutation's completion own
                    // the next UI state instead of prompting a second time.
                    return
                }
                let acceptedByID = Dictionary(uniqueKeysWithValues: accepted.map { ($0.id, $0) })
                for (id, controller) in Array(self.windowControllers) {
                    guard let updated = acceptedByID[id] else {
                        self.windowControllers.removeValue(forKey: id)
                        controller.closeAfterProfileDeletion()
                        continue
                    }
                    controller.applyRefreshedSnapshot(updated, allProfiles: accepted)
                }
                self.profileManagerController?.applyRefreshedSnapshot()
            case .failure(let error):
                self.showError("Workspace refresh failed: \(error.localizedDescription)")
            }
        }
    }

    /// Fixed target for the single Support action in the Help menu.
    @objc func openKoFi() {
        NSWorkspace.shared.open(Self.koFiURL)
    }

    func applySavedAppearance() {
        NSApp.appearance = Product.savedAppearance().appearance
    }

    @objc func chooseAppearance(_ sender: NSMenuItem) {
        guard let choice = AppAppearance(rawValue: sender.tag) else { return }
        UserDefaults.standard.set(choice.rawValue, forKey: Product.appearancePreferenceKey)
        NSApp.appearance = choice.appearance
        sender.menu?.items.forEach { $0.state = $0.tag == choice.rawValue ? .on : .off }
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = Product.name
        alert.informativeText = message
        alert.alertStyle = .warning
        alert.runModal()
    }
}
