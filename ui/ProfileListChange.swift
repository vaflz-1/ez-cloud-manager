import Foundation

/// The four mutations that change the app-global profile collection.
/// Profile field edits use `.profileDidChange` instead because they do not
/// add or remove rows from profile pickers.
enum ProfileListMutation: Equatable {
    case created
    case duplicated
    case imported
    case deleted
}

/// Typed payload for `.profileListDidChange`.
///
/// Kept deliberately small: consumers only need to know which row changed
/// and whether the bound profile ceased to exist. A fresh profile snapshot is
/// always fetched through CredentialsService rather than passed between
/// controllers as mutable shared state.
final class ProfileListChange {
    let mutation: ProfileListMutation
    let profileID: String

    init(_ mutation: ProfileListMutation, profileID: String) {
        self.mutation = mutation
        self.profileID = profileID
    }
}

extension Notification.Name {
    /// Posted after a successful create/duplicate/import/delete operation;
    /// object: `ProfileListChange`.
    static let profileListDidChange = Notification.Name("EZCloudManager.profileListDidChange")
}
