package profile

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	// maxImportBytes caps the total (compressed) size of an .ezprofile file.
	maxImportBytes = 20 << 20 // 20 MiB
	// maxImportEntries caps the number of entries a zip may contain.
	maxImportEntries = 256
	// maxEntryUncompressed caps any single entry's decompressed size.
	maxEntryUncompressed = 4 << 20 // 4 MiB
	// maxTotalUncompressed caps the sum of all entries' decompressed sizes —
	// the zip-bomb guard.
	maxTotalUncompressed = 16 << 20 // 16 MiB
)

// importAllowlist is the set of filenames Import will read from a zip.
// Extend it only once a reader exists for the new file — don't relax it to
// "anything goes".
var importAllowlist = map[string]bool{
	"profile.json": true,
}

// Export writes a profile as a `.ezprofile` zip (profile.json at the zip
// root) to w.
func Export(root, id string, w io.Writer) error {
	p, err := Get(root, id)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	zw := zip.NewWriter(w)
	entry, err := zw.Create("profile.json")
	if err != nil {
		return err
	}
	if _, err := entry.Write(data); err != nil {
		return err
	}
	return zw.Close()
}

// Import reads a `.ezprofile` zip and creates a NEW profile from it. The
// imported ID is always discarded and a fresh one assigned (see Create); a
// Name collision with an existing profile is resolved by appending
// " (Imported)", " (Imported 2)", … rather than failing — import must never
// fail on a name clash.
//
// r is treated as untrusted input: total size, entry count, per-entry and
// total decompressed size are all capped, every entry name must satisfy
// filepath.IsLocal (rejects "..", absolute paths), and only files in
// importAllowlist are accepted.
func Import(root string, r io.Reader) (imported Profile, err error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImportBytes+1))
	if err != nil {
		return Profile{}, err
	}
	if len(data) > maxImportBytes {
		return Profile{}, fmt.Errorf("import file exceeds the %d MiB size limit", maxImportBytes>>20)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Profile{}, fmt.Errorf("not a valid .ezprofile file: %w", err)
	}
	if len(zr.File) > maxImportEntries {
		return Profile{}, fmt.Errorf("import file has too many entries (max %d)", maxImportEntries)
	}

	var profileData []byte
	var totalUncompressed uint64
	for _, f := range zr.File {
		if !filepath.IsLocal(f.Name) {
			return Profile{}, fmt.Errorf("import file contains an unsafe path %q", f.Name)
		}
		if !importAllowlist[f.Name] {
			return Profile{}, fmt.Errorf("import file contains an unexpected entry %q", f.Name)
		}
		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > maxTotalUncompressed {
			return Profile{}, errors.New("import file expands beyond the allowed size — refusing (zip-bomb guard)")
		}
		if f.UncompressedSize64 > maxEntryUncompressed {
			return Profile{}, fmt.Errorf("%s exceeds the %d MiB per-entry size limit", f.Name, maxEntryUncompressed>>20)
		}

		rc, err := f.Open()
		if err != nil {
			return Profile{}, err
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxEntryUncompressed+1))
		_ = rc.Close()
		if err != nil {
			return Profile{}, err
		}
		if f.Name == "profile.json" {
			profileData = content
		}
	}
	if profileData == nil {
		return Profile{}, errors.New("import file does not contain profile.json")
	}

	var incoming Profile
	if err := json.Unmarshal(profileData, &incoming); err != nil {
		return Profile{}, fmt.Errorf("parse profile.json: %w", err)
	}

	// Parsing and zip-bomb validation above deliberately happen outside the
	// root lock. Only the name snapshot plus final create need serialization.
	release, err := acquireRootLock(root)
	if err != nil {
		return Profile{}, err
	}
	defer func() {
		if releaseErr := release(); err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	existing, err := List(root)
	if err != nil {
		return Profile{}, err
	}
	incoming.Name = importUniqueName(existing, strings.TrimSpace(incoming.Name))
	// Create ignores whatever ID/Version/timestamps came in (validateProfile
	// never copies them) — an imported profile is always a NEW profile,
	// never a rebind of the exporting machine's ID.
	return createWithRootLockHeld(root, incoming)
}

// importUniqueName appends " (Imported)", " (Imported 2)", … until name is
// free.
func importUniqueName(existing []Profile, name string) string {
	if name == "" {
		name = "Imported profile"
	}
	if !nameTaken(existing, name, "") {
		return name
	}
	if candidate := name + " (Imported)"; !nameTaken(existing, candidate, "") {
		return candidate
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s (Imported %d)", name, n)
		if !nameTaken(existing, candidate, "") {
			return candidate
		}
	}
}
