import AppKit

/// Native review surface for connector-owned SSO/browser authentication.
/// Discovery never mutates local stores; only the explicit Apply action does.
final class ConnectionAuthSyncSheetController: NSWindowController,
    NSTableViewDataSource, NSTableViewDelegate, NSSearchFieldDelegate
{
    private let service: CredentialsService
    private let extraEnv: [String: String]
    private let providers: [ProviderInfo]
    private let workspaceName: String
    private let workspaceIsScoped: Bool
    private let workspaceConnections: Set<AccountRef>
    private let conflictsWithOpenDraft: (String, String) -> Bool
    private let canReviewConflict: (String, String) -> Bool
    private let onReviewConflict: (String, String) -> Void
    /// Returns a non-fatal post-apply warning (for example a Workspace scope
    /// update failure). Connection writes have already succeeded at this point.
    private let onApplied: (String, [String], Bool) -> String?
    private let onApplyFailure: () -> Void
    private let onDismiss: () -> Void

    private var snapshot: ConnectionAuthSnapshot?
    private var selectedIDs: Set<String> = []
    private var filteredCandidates: [ConnectionAuthCandidate] = []
    private var operationGeneration = 0
    private var cancellation: FastProcessCancellation?
    private var applying = false
    private var dismissed = false
    private var closeAfterApply = false
    private var closeParentAfterApply = false

    private let providerPopup = NSPopUpButton()
    private let subtitleLabel = NSTextField(wrappingLabelWithString: "")
    private let searchField = NSSearchField()
    private let tableView = NSTableView()
    private let statusLabel = NSTextField(wrappingLabelWithString: "")
    private let modePopup = NSPopUpButton()
    private let addToWorkspaceCheckbox = NSButton(
        checkboxWithTitle: "Add imported connections to this workspace",
        target: nil,
        action: nil
    )
    private let progress = NSProgressIndicator()
    private let signInButton = NSButton(title: "Sign In…", target: nil, action: nil)
    private let applyButton = NSButton(title: "Apply", target: nil, action: nil)
    private let cancelButton = NSButton(title: "Cancel", target: nil, action: nil)
    private var scrollHeightConstraint: NSLayoutConstraint?
    private weak var rootStack: NSStackView?
    private var blockedPanel: NSStackView?
    private var blockedReviewTarget: (provider: String, name: String)?

    init(
        providerID: String,
        providers: [ProviderInfo],
        workspaceName: String,
        workspaceIsScoped: Bool,
        workspaceConnections: Set<AccountRef>,
        extraEnv: [String: String],
        service: CredentialsService,
        conflictsWithOpenDraft: @escaping (String, String) -> Bool,
        canReviewConflict: @escaping (String, String) -> Bool,
        onReviewConflict: @escaping (String, String) -> Void,
        onApplied: @escaping (String, [String], Bool) -> String?,
        onApplyFailure: @escaping () -> Void,
        onDismiss: @escaping () -> Void
    ) {
        self.service = service
        self.extraEnv = extraEnv
        self.providers = providers.filter { ["aws", "gcp"].contains($0.id) }
        self.workspaceName = workspaceName
        self.workspaceIsScoped = workspaceIsScoped
        self.workspaceConnections = workspaceConnections
        self.conflictsWithOpenDraft = conflictsWithOpenDraft
        self.canReviewConflict = canReviewConflict
        self.onReviewConflict = onReviewConflict
        self.onApplied = onApplied
        self.onApplyFailure = onApplyFailure
        self.onDismiss = onDismiss
        super.init(window: nil)
        buildWindow(initialProviderID: providerID)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    deinit { cancellation?.cancel() }

    var isApplying: Bool { applying }

    func requestCloseAfterApply(closeParent: Bool = false) {
        guard applying else { return }
        closeAfterApply = true
        closeParentAfterApply = closeParentAfterApply || closeParent
        setStatus("Finishing the reviewed connection changes before closing…", announce: true)
    }

    func present(on parent: NSWindow) {
        guard let window else { return }
        parent.beginSheet(window)
        discover()
    }

    /// Parent workspace/window teardown must terminate a browser/device flow,
    /// not merely hide its sheet while the CLI keeps running in the background.
    func dismissAndCancel() {
        if applying {
            // Keep this controller and its parent callback alive until Apply
            // reconciles the workspace links/scope. Hiding it here would let
            // the weak completion disappear after the core write succeeds.
            closeAfterApply = true
            setStatus("Finishing the reviewed connection changes before closing…", announce: true)
            return
        }
        cancelCurrentOperation()
        closeSheet()
    }

    private var currentProvider: String {
        providerPopup.selectedItem?.representedObject as? String ?? providers.first?.id ?? "aws"
    }

    private func buildWindow(initialProviderID: String) {
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 720, height: 430),
            styleMask: [.titled],
            backing: .buffered,
            defer: false
        )
        window.title = "Sign In & Sync Connections"
        window.minSize = NSSize(width: 640, height: 400)
        window.maxSize = NSSize(width: 840, height: 780)
        self.window = window

        let root = NSView()
        root.translatesAutoresizingMaskIntoConstraints = false
        window.contentView = root

        let title = NSTextField(labelWithString: "Sign In & Sync Connections")
        title.font = .systemFont(ofSize: 20, weight: .semibold)
        title.setAccessibilityRole(.staticText)

        subtitleLabel.textColor = .secondaryLabelColor
        subtitleLabel.maximumNumberOfLines = 3

        providerPopup.target = self
        providerPopup.action = #selector(providerChanged)
        for provider in providers {
            providerPopup.addItem(withTitle: provider.displayName)
            providerPopup.lastItem?.representedObject = provider.id
        }
        if let index = providers.firstIndex(where: { $0.id == initialProviderID }) {
            providerPopup.selectItem(at: index)
        }

        searchField.placeholderString = "Filter connections"
        searchField.delegate = self
        searchField.sendsSearchStringImmediately = true

        let topControls = NSStackView(views: [providerPopup, searchField])
        topControls.orientation = .horizontal
        topControls.alignment = .centerY
        topControls.spacing = 10
        providerPopup.setContentHuggingPriority(.required, for: .horizontal)

        tableView.headerView = nil
        tableView.rowHeight = 72
        tableView.backgroundColor = .clear
        tableView.usesAlternatingRowBackgroundColors = false
        tableView.allowsEmptySelection = true
        tableView.addTableColumn(NSTableColumn(identifier: .init("candidate")))
        tableView.dataSource = self
        tableView.delegate = self

        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        scroll.documentView = tableView

        for mode in ConnectionAuthApplyMode.allCases {
            modePopup.addItem(withTitle: mode.title)
            modePopup.lastItem?.representedObject = mode.rawValue
        }
        modePopup.selectItem(at: 0)
        modePopup.target = self
        modePopup.action = #selector(modeChanged)

        addToWorkspaceCheckbox.state = workspaceIsScoped ? .on : .off
        addToWorkspaceCheckbox.isEnabled = workspaceIsScoped
        addToWorkspaceCheckbox.toolTip = workspaceIsScoped
            ? "Also make imported connections visible in “\(workspaceName)”"
            : "This workspace already allows every discovered connection"

        progress.style = .spinning
        progress.controlSize = .small
        progress.isDisplayedWhenStopped = false

        statusLabel.textColor = .secondaryLabelColor
        statusLabel.maximumNumberOfLines = 2
        statusLabel.setAccessibilityElement(true)

        UI.style(signInButton, as: .secondary)
        signInButton.target = self
        signInButton.action = #selector(signIn)
        signInButton.image = NSImage(systemSymbolName: "person.badge.key", accessibilityDescription: nil)
        signInButton.imagePosition = .imageLeading

        UI.style(applyButton, as: .primary)
        applyButton.keyEquivalent = "\r"
        applyButton.target = self
        applyButton.action = #selector(apply)

        UI.style(cancelButton, as: .secondary)
        cancelButton.keyEquivalent = "\u{1b}"
        cancelButton.target = self
        cancelButton.action = #selector(cancelOrClose)

        let statusRow = NSStackView(views: [progress, statusLabel])
        statusRow.orientation = .horizontal
        statusRow.alignment = .centerY
        statusRow.spacing = 8

        let actions = NSStackView(views: [modePopup, NSView(), signInButton, cancelButton, applyButton])
        actions.orientation = .horizontal
        actions.alignment = .centerY
        actions.spacing = 8
        actions.views[1].setContentHuggingPriority(.defaultLow, for: .horizontal)

        let stack = NSStackView(views: [title, subtitleLabel, topControls, scroll, addToWorkspaceCheckbox, statusRow, actions])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 12
        stack.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(stack)
        rootStack = stack

        subtitleLabel.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        topControls.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        searchField.widthAnchor.constraint(greaterThanOrEqualToConstant: 260).isActive = true
        scroll.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        scrollHeightConstraint = scroll.heightAnchor.constraint(equalToConstant: 250)
        scrollHeightConstraint?.isActive = true
        statusRow.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        statusLabel.trailingAnchor.constraint(equalTo: statusRow.trailingAnchor).isActive = true
        actions.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 24),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -24),
            stack.topAnchor.constraint(equalTo: root.topAnchor, constant: 22),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -18)
        ])
        updateProviderExplanation()
        updateControls()
    }

    func numberOfRows(in tableView: NSTableView) -> Int { filteredCandidates.count }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        let identifier = NSUserInterfaceItemIdentifier("connectionAuthCandidate")
        let cell = (tableView.makeView(withIdentifier: identifier, owner: self) as? ConnectionAuthCandidateCell)
            ?? ConnectionAuthCandidateCell(identifier: identifier, target: self, action: #selector(candidateToggled(_:)))
        let candidate = filteredCandidates[row]
        cell.configure(candidate: candidate, checked: selectedIDs.contains(candidate.id), row: row)
        return cell
    }

    func controlTextDidChange(_ obj: Notification) { rebuildFilter() }

    @objc private func candidateToggled(_ sender: NSButton) {
        guard sender.tag >= 0, sender.tag < filteredCandidates.count else { return }
        let candidate = filteredCandidates[sender.tag]
        // A direct checkbox gesture always means "Apply selected". Keeping a
        // bulk mode active made a checked row look selected while Apply used
        // a different set — exactly the kind of broken agency this review UI
        // must avoid.
        if applyMode != .selected {
            modePopup.selectItem(withTitle: ConnectionAuthApplyMode.selected.title)
        }
        if sender.state == .on { selectedIDs.insert(candidate.id) } else { selectedIDs.remove(candidate.id) }
        updateControls()
    }

    @objc private func providerChanged() {
        cancelCurrentOperation()
        snapshot = nil
        selectedIDs.removeAll()
        filteredCandidates.removeAll()
        tableView.reloadData()
        updateProviderExplanation()
        discover()
    }

    private func updateProviderExplanation() {
        if currentProvider == "aws" {
            subtitleLabel.stringValue = "AWS sign-in lists configured IAM Identity Center profiles only. Existing access-key Connections are already in the sidebar. The AWS CLI keeps all SSO tokens."
        } else {
            subtitleLabel.stringValue = "Google Cloud sign-in discovers projects available to the signed-in account. gcloud keeps all OAuth tokens; Kervik receives project metadata only."
        }
        subtitleLabel.setAccessibilityLabel(subtitleLabel.stringValue)
    }

    @objc private func modeChanged() {
        guard let candidates = snapshot?.candidates else { return }
        selectedIDs = Set(candidates.filter { candidate in
            guard candidate.canApply else { return false }
            switch applyMode {
            case .selected: return true
            case .updateAll: return candidate.status == "update" || candidate.status == "unchanged"
            case .addNew: return candidate.status == "new"
            }
        }.map(\.id))
        tableView.reloadData()
        updateControls()
    }

    private var applyMode: ConnectionAuthApplyMode {
        guard let raw = modePopup.selectedItem?.representedObject as? String else { return .selected }
        return ConnectionAuthApplyMode(rawValue: raw) ?? .selected
    }

    private func discover() {
        let provider = currentProvider
        startOperation(status: "Discovering \(provider.uppercased()) connections…") { cancellation in
            try self.service.discoverConnectionAuth(
                provider: provider,
                extraEnv: self.extraEnv,
                cancellation: cancellation
            )
        } completion: { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let snapshot): self.accept(snapshot, announce: true)
            case .failure(let error): self.showOperationError(error)
            }
        }
    }

    @objc private func signIn() {
        let provider = currentProvider
        if provider == "aws", snapshot == nil {
            discover()
            return
        }
        if provider == "aws", selectedIDs.isEmpty {
            setStatus("Select at least one configured AWS SSO profile before signing in.", announce: true)
            return
        }
        let request = ConnectionAuthLoginRequest(
            expectedRevision: snapshot?.revision,
            candidateIDs: Array(selectedIDs).sorted()
        )
        let requestedIDs = selectedIDs
        startOperation(status: "Waiting for \(provider.uppercased()) sign-in in your browser…") { cancellation in
            try self.service.loginConnectionAuth(
                provider: provider,
                request: request,
                extraEnv: self.extraEnv,
                cancellation: cancellation
            )
        } completion: { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(let response):
                if let fresh = response.snapshot {
                    self.accept(
                        fresh,
                        announce: false,
                        preservingSelection: provider == "aws" ? requestedIDs : nil
                    )
                    self.setStatus("Signed in. Review the refreshed connections before applying.", announce: true)
                } else {
                    self.setStatus("Signed in. Refreshing connections…", announce: true)
                    self.discover()
                }
            case .failure(let error): self.showOperationError(error)
            }
        }
    }

    @objc private func apply() {
        guard let snapshot else { return }
        let selected = candidatesForCurrentMode(in: snapshot)
        guard !selected.isEmpty else {
            setStatus("Nothing matches this apply mode.", announce: true)
            return
        }
        if let conflict = selected.first(where: { conflictsWithOpenDraft(currentProvider, $0.name) }) {
            setStatus("“\(conflict.name)” has an unsaved editor draft. Save or discard it before syncing that connection.", announce: true)
            return
        }

        let principals = Set(selected.compactMap(\.principal))
        if currentProvider == "gcp", principals.count > 1 {
            setStatus("Selected projects span more than one identity. Sign in and sync one identity at a time.", announce: true)
            return
        }
        let request = ConnectionAuthApplyRequest(
            expectedRevision: snapshot.revision,
            // AWS linking status is Workspace-owned, so its non-selected
            // modes are resolved here and sent as an explicit candidate set.
            // GCP destination status is core-owned and can apply the mode
            // atomically against its rediscovered configuration snapshot.
            mode: .selected,
            candidateIDs: selected.map(\.id).sorted(),
            principal: principals.first
        )
        let provider = currentProvider
        applying = true
        startOperation(status: "Applying reviewed connection changes…", cancellable: false) { cancellation in
            try self.service.applyConnectionAuth(
                provider: provider,
                request: request,
                extraEnv: self.extraEnv,
                cancellation: cancellation
            )
        } completion: { [weak self] result in
            guard let self else { return }
            self.applying = false
            switch result {
            case .success(let response):
                let names = response.results.map(\.name)
                let warning = self.onApplied(
                    response.provider,
                    names,
                    self.workspaceIsScoped && self.addToWorkspaceCheckbox.state == .on
                )
                if let warning {
                    self.setStatus(
                        "Connections were applied, but \(warning)",
                        announce: true
                    )
                } else if response.provider == "aws" {
                    self.setStatus(
                        "Linked \(response.results.count) reviewed AWS SSO connection(s).",
                        announce: true
                    )
                } else {
                    self.setStatus(
                        "Applied \(response.added) new, \(response.updated) updated, \(response.unchanged) unchanged connection(s).",
                        announce: true
                    )
                }
                self.closeSheet(after: self.closeAfterApply ? 0 : 0.35)
            case .failure(let error):
                // CAS failures are zero-write and filesystem failures are
                // rolled back best-effort. Refresh regardless so the parent
                // never keeps a stale view if an external writer or an
                // unrecoverable rollback failure changed the store.
                self.onApplyFailure()
                self.showOperationError(error)
                if self.closeAfterApply { self.closeSheet() }
            }
        }
    }

    @objc private func cancelOrClose() {
        if applying {
            closeAfterApply = true
            setStatus("Finishing the reviewed connection changes before closing…", announce: true)
            return
        }
        if cancellation != nil {
            cancelCurrentOperation()
            setStatus("Cancelling sign-in…", announce: true)
            return
        }
        closeSheet()
    }

    private func candidatesForCurrentMode(in snapshot: ConnectionAuthSnapshot) -> [ConnectionAuthCandidate] {
        snapshot.candidates.filter { candidate in
            candidate.canApply && selectedIDs.contains(candidate.id)
        }
    }

    private func accept(
        _ snapshot: ConnectionAuthSnapshot,
        announce: Bool,
        preservingSelection requestedIDs: Set<String>? = nil
    ) {
        guard snapshot.provider == currentProvider else { return }
        let normalized: ConnectionAuthSnapshot
        if snapshot.provider == "aws" {
            let candidates = snapshot.candidates.map { candidate -> ConnectionAuthCandidate in
                guard let source = candidate.sourceProfile,
                      workspaceConnections.contains(AccountRef(provider: "aws", account: source))
                else { return candidate }
                return ConnectionAuthCandidate(
                    id: candidate.id,
                    name: candidate.name,
                    displayName: candidate.displayName,
                    sourceProfile: candidate.sourceProfile,
                    authMode: candidate.authMode,
                    principal: candidate.principal,
                    accountID: candidate.accountID,
                    roleName: candidate.roleName,
                    projectID: candidate.projectID,
                    region: candidate.region,
                    status: "unchanged",
                    canApply: candidate.canApply,
                    reason: candidate.reason
                )
            }
            normalized = ConnectionAuthSnapshot(
                protocolVersion: snapshot.protocolVersion,
                provider: snapshot.provider,
                revision: snapshot.revision,
                candidates: candidates,
                warnings: snapshot.warnings
            )
        } else {
            normalized = snapshot
        }
        // Keep the core's actual status. A blocked row is not necessarily a
        // name collision: invalid SSO metadata, delegated credentials or
        // endpoint/trust overrides all require different remediation.
        let displaySnapshot = normalized
        self.snapshot = displaySnapshot
        let applicable = Set(displaySnapshot.candidates.filter(\.canApply).map(\.id))
        selectedIDs = requestedIDs.map { $0.intersection(applicable) } ?? applicable
        updateBlockedPanel(for: displaySnapshot)
        rebuildFilter()
        // Provider switches may change one AWS row into dozens of GCP rows.
        // Re-size for every accepted provider snapshot (never for each search
        // keystroke), while keeping 1...6 rows visible and the remainder
        // scrollable.
        let visibleRows = max(1, min(6, displaySnapshot.candidates.count))
        scrollHeightConstraint?.constant = CGFloat(visibleRows * 72 + 2)
        rootStack?.layoutSubtreeIfNeeded()
        let intrinsicHeight = rootStack?.fittingSize.height ?? 400
        let visibleHeight = window?.sheetParent?.screen?.visibleFrame.height ?? 760
        let desiredHeight = min(max(400, intrinsicHeight + 40), visibleHeight - 72)
        window?.setContentSize(NSSize(width: 720, height: desiredHeight))
        let blocked = displaySnapshot.candidates.count - applicable.count
        let noun = displaySnapshot.provider == "aws" ? "AWS SSO profile" : "GCP project"
        let plural = displaySnapshot.candidates.count == 1 ? "" : "s"
        var summary = "Found \(displaySnapshot.candidates.count) \(noun)\(plural) · \(applicable.count) ready"
        if blocked > 0 { summary += " · \(blocked) blocked" }
        if let warning = displaySnapshot.warnings.first { summary += " · \(warning)" }
        setStatus(summary, announce: announce)
        updateControls()
    }

    private func rebuildFilter() {
        let query = searchField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        filteredCandidates = (snapshot?.candidates ?? []).filter { candidate in
            query.isEmpty
                || candidate.displayName.localizedCaseInsensitiveContains(query)
                || candidate.targetDescription.localizedCaseInsensitiveContains(query)
                || (candidate.roleName?.localizedCaseInsensitiveContains(query) ?? false)
        }
        tableView.reloadData()
        updateControls()
    }

    private func updateBlockedPanel(for snapshot: ConnectionAuthSnapshot) {
        if let blockedPanel {
            rootStack?.removeArrangedSubview(blockedPanel)
            blockedPanel.removeFromSuperview()
        }
        blockedPanel = nil
        blockedReviewTarget = nil

        let blocked = snapshot.candidates.filter { !$0.canApply }
        guard !blocked.isEmpty, let rootStack else { return }

        let icon = NSImageView(image: NSImage(
            systemSymbolName: "exclamationmark.triangle.fill",
            accessibilityDescription: "Blocked connection"
        ) ?? NSImage())
        icon.contentTintColor = .systemOrange
        icon.setContentHuggingPriority(.required, for: .horizontal)

        let headline = NSTextField(labelWithString:
            blocked.count == 1 ? "1 connection is blocked" : "\(blocked.count) connections are blocked")
        headline.font = .systemFont(ofSize: 12, weight: .semibold)
        let remedy = NSTextField(wrappingLabelWithString:
            blocked.first?.reason ?? "Resolve the provider configuration conflict, then discover again.")
        remedy.font = .systemFont(ofSize: 11)
        remedy.textColor = .secondaryLabelColor
        remedy.maximumNumberOfLines = 3
        let copy = NSStackView(views: [headline, remedy])
        copy.orientation = .vertical
        copy.alignment = .leading
        copy.spacing = 3

        var panelViews: [NSView] = [icon, copy]
        if let target = blocked.first(where: { candidate in
            let name = candidate.sourceProfile ?? candidate.name
            return isNameConflict(candidate) && canReviewConflict(snapshot.provider, name)
        }) {
            let review = NSButton(title: "Review Existing Connection…", target: self, action: #selector(reviewBlockedConnection))
            UI.style(review, as: .secondary)
            review.setContentHuggingPriority(.required, for: .horizontal)
            panelViews += [NSView(), review]
            blockedReviewTarget = (snapshot.provider, target.sourceProfile ?? target.name)
        }

        let panel = NSStackView(views: panelViews)
        panel.orientation = .horizontal
        panel.alignment = .top
        panel.spacing = 9
        panel.edgeInsets = NSEdgeInsets(top: 10, left: 10, bottom: 10, right: 10)
        panel.wantsLayer = true
        panel.layer?.cornerRadius = 8
        panel.layer?.backgroundColor = NSColor.systemOrange.withAlphaComponent(0.10).cgColor
        panel.translatesAutoresizingMaskIntoConstraints = false
        rootStack.insertArrangedSubview(panel, at: min(4, rootStack.arrangedSubviews.count))
        panel.widthAnchor.constraint(equalTo: rootStack.widthAnchor).isActive = true
        blockedPanel = panel
    }

    private func isNameConflict(_ candidate: ConnectionAuthCandidate) -> Bool {
        candidate.reason?.localizedCaseInsensitiveContains("same name") == true
            || candidate.reason?.localizedCaseInsensitiveContains("credentials-file profile") == true
    }

    @objc private func reviewBlockedConnection() {
        guard let target = blockedReviewTarget else { return }
        closeSheet()
        DispatchQueue.main.async { [onReviewConflict] in
            onReviewConflict(target.provider, target.name)
        }
    }

    private func startOperation<T>(
        status: String,
        cancellable: Bool = true,
        work: @escaping (FastProcessCancellation) throws -> T,
        completion: @escaping (Result<T, Error>) -> Void
    ) {
        operationGeneration += 1
        let generation = operationGeneration
        let token = FastProcessCancellation()
        cancellation = token
        progress.startAnimation(nil)
        setStatus(status, announce: true)
        updateControls()
        service.runAsync({ try work(token) }) { [weak self] result in
            guard let self, generation == self.operationGeneration else { return }
            self.cancellation = nil
            self.progress.stopAnimation(nil)
            self.updateControls()
            if token.isCancelled {
                self.setStatus("Operation cancelled. No unapplied changes were written.", announce: true)
                return
            }
            completion(result)
        }
        if !cancellable { cancelButton.isEnabled = false }
    }

    private func cancelCurrentOperation() {
        operationGeneration += 1
        cancellation?.cancel()
        cancellation = nil
        progress.stopAnimation(nil)
        updateControls()
    }

    private func showOperationError(_ error: Error) {
        setStatus(error.localizedDescription, announce: true)
        updateControls()
    }

    private func updateControls() {
        let busy = cancellation != nil
        providerPopup.isEnabled = !busy && !applying
        searchField.isEnabled = snapshot != nil && !busy && !applying
        tableView.isEnabled = !busy && !applying
        modePopup.isEnabled = snapshot != nil && !busy && !applying
        let hasApplicableCandidates = snapshot?.candidates.contains(where: \.canApply) ?? false
        let canSignIn = currentProvider == "gcp" || (snapshot != nil && !selectedIDs.isEmpty && hasApplicableCandidates)
        signInButton.isEnabled = canSignIn && !busy && !applying
        signInButton.title = currentProvider == "aws" ? "Sign In to AWS" : "Sign In to Google Cloud"
        let hasApplicable = snapshot.map { !candidatesForCurrentMode(in: $0).isEmpty } ?? false
        applyButton.isEnabled = hasApplicable && !busy && !applying
        let count = snapshot.map { candidatesForCurrentMode(in: $0).count } ?? 0
        applyButton.title = count == 0 ? "Sync" : "Sync \(count)"
        cancelButton.isEnabled = !applying
        cancelButton.title = busy ? "Cancel Sign-In" : "Cancel"
    }

    private func setStatus(_ text: String, announce: Bool) {
        statusLabel.stringValue = text
        statusLabel.toolTip = text
        statusLabel.setAccessibilityLabel(text)
        guard announce else { return }
        NSAccessibility.post(
            element: statusLabel,
            notification: .announcementRequested,
            userInfo: [
                .announcement: text,
                .priority: NSAccessibilityPriorityLevel.medium.rawValue
            ]
        )
    }

    private func closeSheet(after delay: TimeInterval = 0) {
        let close = { [weak self] in
            guard let self, let window = self.window else { return }
            guard !self.dismissed else { return }
            self.dismissed = true
            let parent = window.sheetParent
            parent?.endSheet(window)
            self.onDismiss()
            if self.closeParentAfterApply { parent?.performClose(nil) }
        }
        if delay == 0 { close() } else { DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: close) }
    }
}

private final class ConnectionAuthCandidateCell: NSTableCellView {
    private let checkbox = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    private let nameLabel = NSTextField(labelWithString: "")
    private let subtitle = NSTextField(labelWithString: "")
    private let status = NSTextField(labelWithString: "")

    init(identifier: NSUserInterfaceItemIdentifier, target: AnyObject, action: Selector) {
        super.init(frame: .zero)
        self.identifier = identifier
        checkbox.target = target
        checkbox.action = action
        checkbox.translatesAutoresizingMaskIntoConstraints = false
        nameLabel.font = .systemFont(ofSize: 13, weight: .medium)
        nameLabel.lineBreakMode = .byTruncatingTail
        nameLabel.translatesAutoresizingMaskIntoConstraints = false
        subtitle.textColor = .secondaryLabelColor
        subtitle.font = .systemFont(ofSize: 11)
        subtitle.maximumNumberOfLines = 2
        subtitle.lineBreakMode = .byWordWrapping
        subtitle.translatesAutoresizingMaskIntoConstraints = false
        status.font = .systemFont(ofSize: 11, weight: .medium)
        status.alignment = .right
        status.translatesAutoresizingMaskIntoConstraints = false
        addSubview(checkbox)
        addSubview(nameLabel)
        addSubview(subtitle)
        addSubview(status)
        NSLayoutConstraint.activate([
            checkbox.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 10),
            checkbox.topAnchor.constraint(equalTo: topAnchor, constant: 8),
            checkbox.widthAnchor.constraint(equalToConstant: 18),
            nameLabel.leadingAnchor.constraint(equalTo: checkbox.trailingAnchor, constant: 6),
            nameLabel.centerYAnchor.constraint(equalTo: checkbox.centerYAnchor),
            nameLabel.trailingAnchor.constraint(lessThanOrEqualTo: status.leadingAnchor, constant: -8),
            subtitle.leadingAnchor.constraint(equalTo: nameLabel.leadingAnchor),
            subtitle.topAnchor.constraint(equalTo: nameLabel.bottomAnchor, constant: 3),
            subtitle.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -7),
            subtitle.trailingAnchor.constraint(lessThanOrEqualTo: status.leadingAnchor, constant: -8),
            status.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -10),
            status.centerYAnchor.constraint(equalTo: centerYAnchor),
            status.widthAnchor.constraint(greaterThanOrEqualToConstant: 64)
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func configure(candidate: ConnectionAuthCandidate, checked: Bool, row: Int) {
        checkbox.tag = row
        checkbox.title = ""
        checkbox.state = checked ? .on : .off
        checkbox.isEnabled = candidate.canApply && candidate.status != "conflict"
        nameLabel.stringValue = candidate.displayName
        nameLabel.textColor = .labelColor
        let details = [candidate.targetDescription, candidate.roleName, candidate.principal]
            .compactMap { value in value.flatMap { $0.isEmpty ? nil : $0 } }
            .joined(separator: " · ")
        subtitle.stringValue = candidate.reason ?? details
        let reason = candidate.reason ?? ""
        let nameConflict = reason.localizedCaseInsensitiveContains("same name")
            || reason.localizedCaseInsensitiveContains("credentials-file profile")
        let renderedStatus = candidate.canApply
            ? candidate.statusTitle
            : (nameConflict ? "Name conflict" : "Needs review")
        status.stringValue = renderedStatus
        status.textColor = candidate.canApply ? .secondaryLabelColor : .systemOrange
        checkbox.toolTip = candidate.canApply ? nil : candidate.reason
        checkbox.setAccessibilityLabel(candidate.displayName)
        checkbox.setAccessibilityHelp(candidate.reason ?? "\(details). \(renderedStatus).")
        subtitle.toolTip = candidate.reason ?? details
        setAccessibilityLabel("\(candidate.displayName), \(details), \(renderedStatus)")
    }
}
