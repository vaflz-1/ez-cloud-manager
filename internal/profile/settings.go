package profile

import (
	"encoding/json"
	"errors"
	"fmt"

	"ez-cloud-manager/internal/plugin"
)

const SettingsConflictMarker = "EZCLOUD_PROFILE_SETTINGS_CONFLICT"

var ErrSettingsConflict = errors.New(SettingsConflictMarker + ": workspace settings changed since draft was loaded")

// CloudAccountsSettings is the JSON shape stored at
// Profile.Settings[plugin.CloudAccountsID] — the Cloud Accounts plugin's own
// account-scoping choice, moved off the core Profile by P1.5 per
// docs/PLATFORM.md principle 5 ("core owns no plugin data"). This is the one
// settings key internal/profile documents and validates (see
// normalizeCloudAccountsBlob in validate.go); it exists purely to carry the
// pre-P1.5 Accounts/ShowAllAccounts migration losslessly (see readProfile's
// Version<3 block) — every other plugin's settings blob stays opaque,
// unvalidated JSON as far as this package is concerned.
//
// The matching Swift shape is decoded only by the Cloud Accounts feature;
// other addons must use provider rails or explicit contribution contracts
// instead of reading this namespace.
type CloudAccountsSettings struct {
	ShowAllAccounts bool         `json:"showAllAccounts"`
	Accounts        []AccountRef `json:"accounts"`
}

// GetCloudAccountsSettings returns p's cloud-accounts settings, or the zero
// value (no scoping accounts, ShowAllAccounts false) if the profile carries
// no such blob or it fails to parse — a malformed or foreign blob must never
// error a caller that just wants to know the current scope.
func GetCloudAccountsSettings(p Profile) CloudAccountsSettings {
	raw, ok := p.Settings[plugin.CloudAccountsID]
	if !ok {
		return CloudAccountsSettings{}
	}
	var s CloudAccountsSettings
	if err := json.Unmarshal(raw, &s); err != nil {
		return CloudAccountsSettings{}
	}
	return s
}

// AllowsConnection reports whether workspace p may use ref. Connection
// stores are machine-wide, but a Workspace is an isolated allowlist over
// those stores: missing or malformed settings allow nothing, and access to
// every discovered Connection requires an explicit ShowAllAccounts opt-in.
//
// The input is normalized with the same rules used when settings are saved so
// callers cannot bypass an allowlist with whitespace or malformed identifiers.
func AllowsConnection(p Profile, ref AccountRef) bool {
	normalized, err := normalizeAccounts([]AccountRef{ref})
	if err != nil || len(normalized) != 1 {
		return false
	}
	ref = normalized[0]
	settings := GetCloudAccountsSettings(p)
	if settings.ShowAllAccounts {
		return true
	}
	for _, allowed := range settings.Accounts {
		if allowed == ref {
			return true
		}
	}
	return false
}

// AddConnectionRef atomically adds ref to the latest Workspace policy. It
// takes the profiles root lock before the per-profile lock so it serializes
// with root-wide Connection deletion cleanup without replacing unrelated
// settings written by another process.
func AddConnectionRef(root, id string, ref AccountRef) (saved Profile, err error) {
	return AddConnectionRefVerified(root, id, ref, nil)
}

// AddConnectionRefVerified is AddConnectionRef with a latest-Workspace
// precondition evaluated while the profiles root lock is held. The CLI uses
// it to prove the named Connection exists in that Workspace's resolved store
// immediately before granting it, preventing arbitrary/future-name grants.
func AddConnectionRefVerified(root, id string, ref AccountRef, verify func(Profile) error) (saved Profile, err error) {
	ref, err = normalizeConnectionRef(ref)
	if err != nil {
		return Profile{}, err
	}
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	current, err := Get(root, id)
	if err != nil {
		return Profile{}, err
	}
	if verify != nil {
		if err := verify(current); err != nil {
			return Profile{}, err
		}
	}
	if GetCloudAccountsSettings(current).ShowAllAccounts {
		// Show-all is already the complete grant. Do not accumulate invisible
		// name refs that could become active later when show-all is disabled.
		return current, nil
	}
	return mutateConnectionRef(root, id, ref, true)
}

// RemoveConnectionRef atomically removes ref from the latest Workspace
// policy. ShowAllAccounts is intentionally preserved: this operation removes
// an explicit grant, while changing the workspace-wide policy remains a
// separate user decision.
func RemoveConnectionRef(root, id string, ref AccountRef) (saved Profile, err error) {
	ref, err = normalizeConnectionRef(ref)
	if err != nil {
		return Profile{}, err
	}
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return mutateConnectionRef(root, id, ref, false)
}

// RemoveConnectionRefFromMatching removes a deleted Connection's explicit
// grant only from Workspaces whose provider store matches the deleted store.
// Connection names are not globally unique: two Workspaces may both contain
// "prod" while resolving AWS_SHARED_CREDENTIALS_FILE (or CLOUDSDK_CONFIG) to
// different stores. The caller owns that provider-specific identity check.
//
// The root lock spans both matching and mutation and follows the same
// root-then-profile order as AddConnectionRef/RemoveConnectionRef, so an
// incremental grant mutation cannot be lost behind cleanup's latest snapshot.
func RemoveConnectionRefFromMatching(root string, ref AccountRef, matches func(Profile) (bool, error)) (err error) {
	ref, err = normalizeConnectionRef(ref)
	if err != nil {
		return err
	}
	if matches == nil {
		return errors.New("workspace store matcher is required")
	}
	release, err := acquireRootLock(root)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	return removeConnectionRefFromMatchingWithRootLockHeld(root, ref, matches)
}

// WithConnectionPolicyLock executes fn while holding the stable profiles root
// lock. Provider operations use this boundary to keep a freshly loaded
// Workspace authorization and the corresponding machine-wide Connection
// read/mutation in one critical section. This closes the authorize-then-use
// gap where a same-named Connection could otherwise be deleted and recreated
// between two CLI processes.
//
// fn must not call an operation that acquires the profiles root lock again.
func WithConnectionPolicyLock(root string, fn func() error) (err error) {
	if fn == nil {
		return errors.New("connection policy transaction is required")
	}
	release, err := acquireRootLock(root)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return fn()
}

// RemoveConnectionRefFromMatchingWithPolicyLockHeld is the transaction form
// of RemoveConnectionRefFromMatching. The caller must already hold the lock
// through WithConnectionPolicyLock; keeping provider delete/create and grant
// cleanup under that same lock prevents authorization resurrection.
func RemoveConnectionRefFromMatchingWithPolicyLockHeld(root string, ref AccountRef, matches func(Profile) (bool, error)) error {
	ref, err := normalizeConnectionRef(ref)
	if err != nil {
		return err
	}
	if matches == nil {
		return errors.New("workspace store matcher is required")
	}
	return removeConnectionRefFromMatchingWithRootLockHeld(root, ref, matches)
}

func removeConnectionRefFromMatchingWithRootLockHeld(root string, ref AccountRef, matches func(Profile) (bool, error)) error {
	profiles, err := List(root)
	if err != nil {
		return err
	}
	for _, workspace := range profiles {
		matched, matchErr := matches(workspace)
		if matchErr != nil {
			return fmt.Errorf("resolve Connection store for workspace %q: %w", workspace.ID, matchErr)
		}
		if !matched || !containsExplicitConnectionRef(GetCloudAccountsSettings(workspace).Accounts, ref) {
			continue
		}
		if _, err := mutateConnectionRef(root, workspace.ID, ref, false); err != nil {
			return fmt.Errorf("clean connection reference from workspace %q: %w", workspace.ID, err)
		}
	}
	return nil
}

func containsExplicitConnectionRef(refs []AccountRef, ref AccountRef) bool {
	for _, candidate := range refs {
		if candidate == ref {
			return true
		}
	}
	return false
}

func normalizeConnectionRef(ref AccountRef) (AccountRef, error) {
	normalized, err := normalizeAccounts([]AccountRef{ref})
	if err != nil {
		return AccountRef{}, err
	}
	if len(normalized) != 1 {
		return AccountRef{}, fmt.Errorf("connection reference is required")
	}
	return normalized[0], nil
}

// mutateConnectionRef requires the caller to hold the profiles root lock.
// mutateProfile adds the target's per-profile lock and always reloads the
// latest settings before applying this one-reference patch.
func mutateConnectionRef(root, id string, ref AccountRef, add bool) (Profile, error) {
	return mutateProfile(root, id, func(current *Profile) error {
		settings := GetCloudAccountsSettings(*current)
		refs := make([]AccountRef, 0, len(settings.Accounts)+1)
		found := false
		for _, existing := range settings.Accounts {
			if existing == ref {
				found = true
				if !add {
					continue
				}
			}
			refs = append(refs, existing)
		}
		if add && !found {
			refs = append(refs, ref)
		}
		settings.Accounts = refs
		blob, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		current.Settings = cloneSettings(current.Settings)
		if current.Settings == nil {
			current.Settings = map[string]json.RawMessage{}
		}
		current.Settings[plugin.CloudAccountsID] = blob
		return nil
	})
}

// GetSettingsBlob returns the raw settings blob for pluginID, or nil if the
// profile carries none — the generic (any plugin id) accessor backing
// `ezcloud profile settings get`.
func GetSettingsBlob(p Profile, pluginID string) json.RawMessage {
	return p.Settings[pluginID]
}

// SetSettingsBlob atomically replaces (or adds) one plugin's settings
// namespace on the latest profile — the generic accessor backing `ezcloud
// profile settings set`. Concurrent core/plugin mutations are preserved. raw
// must already be the exact bytes to store; validation of a KNOWN plugin id's
// shape (currently only cloud-accounts) happens inside the locked mutation via
// normalizeSettings.
func SetSettingsBlob(root, id, pluginID string, raw json.RawMessage) (Profile, error) {
	return mutateProfile(root, id, func(p *Profile) error {
		p.Settings = cloneSettings(p.Settings)
		if p.Settings == nil {
			p.Settings = map[string]json.RawMessage{}
		}
		p.Settings[pluginID] = raw
		return nil
	})
}

// SetCloudAccountsSettingsIfUnchanged replaces the complete Connection policy
// only when expectedUpdatedAt still matches the latest Workspace. It shares
// the root→profile lock order with connection-ref cleanup, preventing a stale
// Visible Connections sheet from resurrecting a deleted grant.
func SetCloudAccountsSettingsIfUnchanged(root, id string, raw json.RawMessage, expectedUpdatedAt string) (saved Profile, err error) {
	return SetCloudAccountsSettingsIfUnchangedVerified(root, id, raw, expectedUpdatedAt, nil)
}

// SetCloudAccountsSettingsIfUnchangedVerified adds a root-lock-held
// precondition for every explicit Connection reference. The CLI supplies a
// store-aware verifier so a full Visible Connections save cannot create a
// grant for a missing/future name that might later be populated by SSO or an
// external writer. Package-only callers may omit it through the compatibility
// wrapper above when they are testing normalization rather than the CLI trust
// boundary.
func SetCloudAccountsSettingsIfUnchangedVerified(
	root, id string,
	raw json.RawMessage,
	expectedUpdatedAt string,
	verify func(Profile, CloudAccountsSettings) error,
) (saved Profile, err error) {
	if expectedUpdatedAt == "" {
		return Profile{}, fmt.Errorf("expected workspace updatedAt is required")
	}
	var proposed CloudAccountsSettings
	if err := json.Unmarshal(raw, &proposed); err != nil {
		return Profile{}, fmt.Errorf("decode cloud-accounts settings: %w", err)
	}
	proposed.Accounts, err = normalizeAccounts(proposed.Accounts)
	if err != nil {
		return Profile{}, err
	}
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()
	return mutateProfile(root, id, func(p *Profile) error {
		if p.UpdatedAt != expectedUpdatedAt {
			return fmt.Errorf("%w: workspace %q changed; reload its Connection policy before saving", ErrSettingsConflict, id)
		}
		if verify != nil {
			if err := verify(*p, proposed); err != nil {
				return err
			}
		}
		p.Settings = cloneSettings(p.Settings)
		if p.Settings == nil {
			p.Settings = map[string]json.RawMessage{}
		}
		normalizedRaw, err := json.Marshal(proposed)
		if err != nil {
			return err
		}
		p.Settings[plugin.CloudAccountsID] = normalizedRaw
		return nil
	})
}

// cloneSettings returns a shallow copy of in — a fresh map sharing the same
// (immutable-by-convention) json.RawMessage byte slices. Used wherever a
// Profile's Settings map is copied into a new Profile (Duplicate,
// SetSettingsBlob) so the copy never aliases the source's map.
func cloneSettings(in map[string]json.RawMessage) map[string]json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
