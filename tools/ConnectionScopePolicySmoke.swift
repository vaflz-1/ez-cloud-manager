import Foundation

@main
struct ConnectionScopePolicySmoke {
    private static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() {
            fputs("FAIL: \(message)\n", stderr)
            exit(1)
        }
    }

    private static func workspace(settings: [String: JSONValue]?) -> Profile {
        Profile(
            id: "workspace",
            name: "Workspace",
            envVars: [],
            enabledPlugins: [],
            settings: settings,
            windowState: nil,
            version: 4,
            createdAt: "2026-08-13T00:00:00Z",
            savedAt: "2026-08-13T00:00:00Z",
            updatedAt: "2026-08-13T00:00:00Z"
        )
    }

    private static func encodedSettings(_ settings: CloudAccountsSettings) -> JSONValue {
        let data = try! JSONEncoder().encode(settings)
        return try! JSONDecoder().decode(JSONValue.self, from: data)
    }

    static func main() {
        let missing = workspace(settings: nil)
        require(!missing.allowsConnection(provider: "aws", account: "prod"), "missing policy must fail closed")

        let malformed = workspace(settings: [
            PluginID.cloudAccounts: .object(["accounts": .string("not-an-array")])
        ])
        require(!malformed.allowsConnection(provider: "aws", account: "prod"), "malformed policy must fail closed")

        let explicitAll = workspace(settings: [
            PluginID.cloudAccounts: encodedSettings(CloudAccountsSettings(showAllAccounts: true))
        ])
        require(explicitAll.allowsConnection(provider: "aws", account: "prod"), "explicit show-all must remain supported")

        let allowList = workspace(settings: [
            PluginID.cloudAccounts: encodedSettings(CloudAccountsSettings(
                accounts: [AccountRef(provider: "aws", account: "prod")]
            ))
        ])
        require(allowList.allowsConnection(provider: "aws", account: "prod"), "allow-listed connection was denied")
        require(!allowList.allowsConnection(provider: "aws", account: "staging"), "unlisted account was allowed")
        require(!allowList.allowsConnection(provider: "gcp", account: "prod"), "provider identity was ignored")

        let discovered = [
            ProfileSummary(name: "prod", keys: []),
            ProfileSummary(name: "staging", keys: [])
        ]
        require(
            allowList.filterConnections(discovered, provider: "aws").map(\.name) == ["prod"],
            "connection filtering did not preserve the allow-list"
        )

        print("Connection scope policy smoke: PASS")
    }
}
