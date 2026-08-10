import Foundation

@main
struct ProfileSnapshotSmoke {
    private static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() {
            fputs("FAIL: \(message)\n", stderr)
            exit(1)
        }
    }

    private static func profile(name: String, updatedAt: String) -> Profile {
        Profile(
            id: "workspace",
            name: name,
            envVars: [],
            enabledPlugins: [],
            settings: nil,
            windowState: nil,
            version: 4,
            createdAt: "2026-08-11T00:00:00Z",
            savedAt: updatedAt,
            updatedAt: updatedAt
        )
    }

    static func main() {
        let service = CredentialsService()
        let old = profile(name: "old", updatedAt: "2026-08-11T00:00:00Z")
        let localMutation = profile(name: "new", updatedAt: "2026-08-11T00:00:01Z")
        let external = profile(name: "external", updatedAt: "2026-08-11T00:00:02Z")

        let initialRead = service.beginCompleteProfileRead()
        require(
            service.commitCompleteProfiles([old], ifUnchangedSince: initialRead) != nil,
            "initial complete snapshot was accepted"
        )
        let staleRead = service.beginCompleteProfileRead()
        service.storeProfile(localMutation)
        require(
            service.commitCompleteProfiles([old], ifUnchangedSince: staleRead) == nil,
            "a stale complete read must not overwrite an in-flight mutation"
        )
        require(service.cachedProfile(id: old.id)?.name == "new", "newer cache value was preserved")

        let currentRead = service.beginCompleteProfileRead()
        let accepted = service.commitCompleteProfiles([external], ifUnchangedSince: currentRead)
        require(accepted?.count == 1, "current complete read was accepted")
        require(accepted?.first?.name == "external", "caller receives the accepted snapshot")
        require(service.cachedProfile(id: old.id)?.name == "external", "accepted snapshot reached cache")

        let beforeDelete = service.beginCompleteProfileRead()
        service.removeCachedProfile(id: old.id)
        require(
            service.commitCompleteProfiles([external], ifUnchangedSince: beforeDelete) == nil,
            "a stale read must not resurrect a deleted workspace"
        )

        print("Profile snapshot smoke: PASS")
    }
}
