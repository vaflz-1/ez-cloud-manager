// Package flatjson flattens arbitrary decoded JSON (map[string]any / []any
// trees, as produced by encoding/json into an `any`) into a flat map of dotted
// string paths to string values, and reconstructs an edited tree from such a
// map. It exists so a launch-template editor can present nested AWS data as a
// simple key/value table and apply a user's edits back onto the original
// document without losing the value types AWS is strict about.
//
// Path grammar: map keys are joined with '.', array elements are addressed with
// a bracketed index, e.g. "NetworkInterfaces[0].DeviceIndex". This assumes map
// keys never contain a literal '.' or '[' — which holds for AWS launch template
// data, whose keys are PascalCase identifiers — so a path is unambiguous. Keys
// with literal dots are therefore not supported and do not occur in the domain.
package flatjson

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Flatten renders v into path -> string pairs. Leaves are rendered so the
// result can round-trip through Unflatten: strings verbatim, numbers via
// FormatFloat (shortest form, so integral values render without a fractional
// part), bools as "true"/"false", and null as "". Empty maps and empty arrays
// contribute no entries because they have no leaves to address.
func Flatten(v any) map[string]string {
	out := map[string]string{}
	flatten("", v, out)
	return out
}

func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			flatten(joinKey(prefix, key), child, out)
		}
	case []any:
		for i, child := range t {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), child, out)
		}
	default:
		out[prefix] = render(v)
	}
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// render turns a JSON leaf into its display string. json.Unmarshal into `any`
// yields float64 for every number; json.Number is handled too in case a caller
// decoded with UseNumber.
func render(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	case string:
		return t
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// Unflatten returns a deep copy of source with the value at each edited path
// replaced. Coercion depends on whether the path already exists in source:
//
//   - Existing path: the string is coerced to the ORIGINAL leaf type. A number
//     is parsed with ParseFloat, a bool must be exactly "true"/"false", a string
//     is taken verbatim, and a null keeps the string unless it is empty (empty
//     stays null). Setting any existing non-null path to "" instead removes it
//     from its parent — deleting the key for maps, nulling the slot for arrays.
//   - New path: intermediate maps/arrays are created as needed. An array index
//     may append (index == len) but not skip (index > len is an error). The new
//     value is typed as bool only for exact "true"/"false"; everything else is
//     kept as a string, deliberately WITHOUT numeric guessing so that identifier
//     strings like "8080" are not silently turned into numbers. An empty string
//     for a new path is skipped rather than creating an empty leaf.
//
// Edits are applied in a natural (numeric-aware) path order so that appending
// several new array elements in one call resolves in ascending index order.
func Unflatten(source any, edits map[string]string) (any, error) {
	result := deepCopy(source)

	keys := make([]string, 0, len(edits))
	for k := range edits {
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool { return naturalLess(keys[i], keys[j]) })

	for _, path := range keys {
		value := edits[path]
		segs, err := parsePath(path)
		if err != nil {
			return nil, fmt.Errorf("path %q: %w", path, err)
		}
		if len(segs) == 0 {
			return nil, fmt.Errorf("path %q: empty paths are not supported", path)
		}

		orig, exists := lookup(source, segs)
		if exists {
			result, err = applyExisting(result, segs, path, orig, value)
		} else {
			result, err = applyNew(result, segs, value)
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// applyExisting replaces or removes a path already present in source, coercing
// value to the original leaf type.
func applyExisting(result any, segs []segment, path string, orig any, value string) (any, error) {
	switch orig.(type) {
	case nil:
		// A null keeps its slot; an empty edit means "still null".
		if value == "" {
			return setPath(result, segs, nil)
		}
		return setPath(result, segs, value)
	case bool:
		if value == "" {
			return result, removePath(result, segs)
		}
		switch value {
		case "true":
			return setPath(result, segs, true)
		case "false":
			return setPath(result, segs, false)
		default:
			return nil, fmt.Errorf("path %q: cannot coerce %q to bool (want \"true\" or \"false\")", path, value)
		}
	case float64, json.Number:
		if value == "" {
			return result, removePath(result, segs)
		}
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("path %q: cannot coerce %q to number", path, value)
		}
		return setPath(result, segs, f)
	case string:
		if value == "" {
			return result, removePath(result, segs)
		}
		return setPath(result, segs, value)
	default:
		// Original value is an object or array. Only removal is meaningful.
		if value == "" {
			return result, removePath(result, segs)
		}
		return nil, fmt.Errorf("path %q: cannot replace an object or array with a scalar value", path)
	}
}

// applyNew creates a path that did not exist in source.
func applyNew(result any, segs []segment, value string) (any, error) {
	if value == "" {
		return result, nil
	}
	var newVal any
	switch value {
	case "true":
		newVal = true
	case "false":
		newVal = false
	default:
		newVal = value
	}
	return setPath(result, segs, newVal)
}

// segment is one component of a parsed path: either a map key or an array index.
type segment struct {
	key   string
	index int
	isIdx bool
}

func parsePath(path string) ([]segment, error) {
	var segs []segment
	i, n := 0, len(path)
	for i < n {
		if path[i] == '[' {
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("unterminated '[' in path")
			}
			num := path[i+1 : i+end]
			idx, err := strconv.Atoi(num)
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("invalid array index %q", num)
			}
			segs = append(segs, segment{index: idx, isIdx: true})
			i += end + 1
			if i < n && path[i] == '.' {
				i++
			}
			continue
		}
		start := i
		for i < n && path[i] != '.' && path[i] != '[' {
			i++
		}
		segs = append(segs, segment{key: path[start:i]})
		if i < n && path[i] == '.' {
			i++
		}
	}
	return segs, nil
}

// lookup walks root and reports the value at segs and whether it exists. It is
// run against the ORIGINAL source so coercion keys off the untouched type.
func lookup(root any, segs []segment) (any, bool) {
	cur := root
	for _, s := range segs {
		if s.isIdx {
			arr, ok := cur.([]any)
			if !ok || s.index < 0 || s.index >= len(arr) {
				return nil, false
			}
			cur = arr[s.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := m[s.key]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// setPath writes value at segs, creating intermediate maps/arrays as needed and
// growing arrays by exactly one when the index equals the current length. It
// returns the (possibly reallocated) container so a grown root slice propagates.
func setPath(container any, segs []segment, value any) (any, error) {
	s := segs[0]
	last := len(segs) == 1

	if s.isIdx {
		arr, ok := container.([]any)
		if container == nil {
			arr = []any{}
		} else if !ok {
			return nil, fmt.Errorf("cannot index element %d of a non-array", s.index)
		}
		if s.index < 0 || s.index > len(arr) {
			return nil, fmt.Errorf("array index %d out of range (length %d)", s.index, len(arr))
		}
		if s.index == len(arr) {
			arr = append(arr, nil)
		}
		if last {
			arr[s.index] = value
			return arr, nil
		}
		child, err := setPath(arr[s.index], segs[1:], value)
		if err != nil {
			return nil, err
		}
		arr[s.index] = child
		return arr, nil
	}

	m, ok := container.(map[string]any)
	if container == nil {
		m = map[string]any{}
	} else if !ok {
		return nil, fmt.Errorf("cannot set key %q on a non-object", s.key)
	}
	if last {
		m[s.key] = value
		return m, nil
	}
	child, err := setPath(m[s.key], segs[1:], value)
	if err != nil {
		return nil, err
	}
	m[s.key] = child
	return m, nil
}

// removePath deletes the leaf at segs. The path is known to exist (verified via
// lookup), so navigation cannot miss. Map leaves are deleted from their parent;
// array leaves are set to null to preserve sibling indices.
func removePath(container any, segs []segment) error {
	s := segs[0]
	if len(segs) == 1 {
		if s.isIdx {
			arr, ok := container.([]any)
			if !ok || s.index < 0 || s.index >= len(arr) {
				return fmt.Errorf("array index %d out of range", s.index)
			}
			arr[s.index] = nil
			return nil
		}
		m, ok := container.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot remove key %q from a non-object", s.key)
		}
		delete(m, s.key)
		return nil
	}
	if s.isIdx {
		arr, ok := container.([]any)
		if !ok || s.index < 0 || s.index >= len(arr) {
			return fmt.Errorf("array index %d out of range", s.index)
		}
		return removePath(arr[s.index], segs[1:])
	}
	m, ok := container.(map[string]any)
	if !ok {
		return fmt.Errorf("cannot descend key %q of a non-object", s.key)
	}
	return removePath(m[s.key], segs[1:])
}

// deepCopy clones the decoded-JSON tree so edits never mutate the caller's
// source. Scalars are immutable and shared as-is.
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, val := range t {
			s[i] = deepCopy(val)
		}
		return s
	default:
		return v
	}
}

// naturalLess orders paths so that embedded numbers compare by value, keeping
// "arr[2]" before "arr[10]". This matters when several appends land in one call.
func naturalLess(a, b string) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if isDigit(a[i]) && isDigit(b[j]) {
			si, sj := i, j
			for i < len(a) && isDigit(a[i]) {
				i++
			}
			for j < len(b) && isDigit(b[j]) {
				j++
			}
			na := strings.TrimLeft(a[si:i], "0")
			nb := strings.TrimLeft(b[sj:j], "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if a[i] != b[j] {
			return a[i] < b[j]
		}
		i++
		j++
	}
	return len(a)-i < len(b)-j
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
