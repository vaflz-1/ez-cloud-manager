# Security policy

## Reporting a vulnerability

Please do not disclose a suspected vulnerability in a public issue, discussion,
or pull request. Use [GitHub's private vulnerability report](https://github.com/vaflz-1/ez-cloud-manager/security/advisories/new) instead.

Include the affected version or commit, the smallest safe reproduction, impact,
and any suggested mitigation. Do not include real cloud credentials, customer
data, or access tokens. A synthetic profile and redacted logs are preferred.

We aim to acknowledge a report within 72 hours, provide an initial assessment
within 7 days, and coordinate disclosure after a fix is available. Timelines may
change for complex issues, but reporters will receive status updates.

## Supported versions

Security fixes target the current `main` branch and the latest 2.x build. The
legacy 1.x preview is unsupported and should not be used for sensitive work.

## Security model

Kervik is local-first and intentionally has no telemetry. Credentials remain in
the native provider stores selected by the user. Profiles, add-on manifests and
audit records must not contain credential values. The currently bundled add-ons
and connectors are compiled, trusted components; external executable add-ons
and the capability broker described in the platform roadmap are not shipped yet.

Local builds are ad-hoc signed for development. They are not a notarized public
distribution and must not be presented as Gatekeeper-ready until a Developer ID
release pipeline and notarization are in place.
