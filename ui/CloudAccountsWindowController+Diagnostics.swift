import AppKit

/// Test Connection: runs the provider's own vendor-CLI identity check for the
/// selected, saved account and renders one of four states in Card 1's
/// connectivity row (see CloudAccountsWindowController+Layout.swift for the
/// view construction) — idle, testing (spinner), success (green check +
/// identity summary), failure (red mark + short reason + a "Details" link
/// opening the full error in a popover). Offered for every provider, even one
/// that doesn't implement Check yet (azure) — the result reports that
/// cleanly instead of the button vanishing (docs/PLATFORM.md principle 6).
extension CloudAccountsWindowController {
    /// Idle state: no result shown yet (or the selection just changed, which
    /// must never leave a stale result from a different account on screen).
    func resetConnectionTestState() {
        connectionTestFullError = nil
        testConnectionSpinner.stopAnimation(nil)
        testConnectionResultIcon.image = nil
        testConnectionResultLabel.stringValue = ""
        testConnectionDetailsButton.isHidden = true
        testConnectionButton.isEnabled = selectedProfileName != nil
    }

    private func setConnectionTesting() {
        connectionTestFullError = nil
        testConnectionButton.isEnabled = false
        testConnectionSpinner.startAnimation(nil)
        testConnectionResultIcon.image = nil
        testConnectionResultLabel.stringValue = "Checking…"
        testConnectionResultLabel.textColor = .secondaryLabelColor
        testConnectionDetailsButton.isHidden = true
    }

    private func setConnectionSuccess(_ summary: String) {
        testConnectionSpinner.stopAnimation(nil)
        testConnectionButton.isEnabled = true
        let cfg = NSImage.SymbolConfiguration(pointSize: 12, weight: .medium)
        testConnectionResultIcon.image = NSImage(systemSymbolName: "checkmark.circle.fill", accessibilityDescription: "Connected")?
            .withSymbolConfiguration(cfg)
        testConnectionResultIcon.contentTintColor = .systemGreen
        testConnectionResultLabel.stringValue = summary
        testConnectionResultLabel.textColor = .labelColor
        testConnectionDetailsButton.isHidden = true
    }

    private func setConnectionFailure(_ shortReason: String, fullText: String) {
        testConnectionSpinner.stopAnimation(nil)
        testConnectionButton.isEnabled = true
        let cfg = NSImage.SymbolConfiguration(pointSize: 12, weight: .medium)
        testConnectionResultIcon.image = NSImage(systemSymbolName: "xmark.octagon.fill", accessibilityDescription: "Failed")?
            .withSymbolConfiguration(cfg)
        testConnectionResultIcon.contentTintColor = .systemRed
        testConnectionResultLabel.stringValue = shortReason
        testConnectionResultLabel.textColor = .labelColor
        connectionTestFullError = fullText
        testConnectionDetailsButton.isHidden = (fullText == shortReason)
    }

    @objc func testConnection() {
        guard let name = selectedProfileName else {
            showError("Select an account first.")
            return
        }
        let provider = selectedProvider
        setConnectionTesting()
        service.runAsync({
            try self.service.check(provider: provider, name, extraEnv: self.profile.envVars.asDictionary())
        }) { [weak self] result in
            guard let self, self.selectedProfileName == name, self.selectedProvider == provider else { return }
            switch result {
            case .success(let check):
                if check.ok {
                    self.setConnectionSuccess(Self.formatIdentity(provider: provider, identity: check.identity ?? [:]))
                } else {
                    let reason = check.error ?? "Connection failed"
                    self.setConnectionFailure(Self.shortened(reason), fullText: reason)
                }
            case .failure(let error):
                let message = error.localizedDescription
                self.setConnectionFailure(Self.shortened(message), fullText: message)
            }
        }
    }

    /// The full error text behind the short inline label, in a small popover
    /// anchored to the "Details" link — the guided-errors principle applies
    /// here too: never truncate the only copy of the diagnostic.
    @objc func showConnectionErrorDetails(_ sender: NSButton) {
        guard let text = connectionTestFullError else { return }
        let textView = NSTextView(frame: NSRect(x: 0, y: 0, width: 360, height: 120))
        textView.string = text
        textView.isEditable = false
        textView.drawsBackground = false
        textView.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        textView.textContainerInset = NSSize(width: 10, height: 10)

        let scroll = NSScrollView(frame: textView.frame)
        scroll.hasVerticalScroller = true
        scroll.documentView = textView
        scroll.drawsBackground = false

        let viewController = NSViewController()
        viewController.view = scroll
        let popover = NSPopover()
        popover.contentViewController = viewController
        popover.behavior = .transient
        popover.show(relativeTo: sender.bounds, of: sender, preferredEdge: .maxY)
    }

    /// Per-provider identity summary formatting for the success state.
    /// AWS: the ARN plus the account id restated for at-a-glance scanning.
    /// GCP: the authenticated account email plus the configuration's own
    /// stored project (read from disk by gcpprovider.Check, not a second
    /// live gcloud call).
    private static func formatIdentity(provider: String, identity: [String: String]) -> String {
        switch provider {
        case "aws":
            let arn = identity["arn"] ?? ""
            let account = identity["account"] ?? ""
            if !arn.isEmpty, !account.isEmpty { return "\(arn) · acct \(account)" }
            return arn.isEmpty ? "Connected" : arn
        case "gcp":
            let account = identity["account"] ?? ""
            if let project = identity["project"], !project.isEmpty {
                return account.isEmpty ? "Project \(project)" : "\(account) · project \(project)"
            }
            return account.isEmpty ? "Connected" : account
        default:
            return identity.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }.joined(separator: ", ")
        }
    }

    /// A short, single-line reason for the inline label — the popover always
    /// carries the full text regardless of truncation here.
    private static func shortened(_ text: String, limit: Int = 64) -> String {
        let oneLine = text.split(separator: "\n").first.map(String.init) ?? text
        guard oneLine.count > limit else { return oneLine }
        return String(oneLine.prefix(limit - 1)) + "…"
    }
}
