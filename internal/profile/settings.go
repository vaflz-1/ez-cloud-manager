package profile

import (
	"encoding/json"

	"ez-cloud-manager/internal/plugin"
)

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
	_ = json.Unmarshal(raw, &s)
	return s
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
