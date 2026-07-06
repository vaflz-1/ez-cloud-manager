// Package export renders a profile's fields in formats DevOps tools actually
// consume: shell `export` lines, .env files, provider-native INI blocks and
// JSON. Rendering is schema-driven — the provider's FieldSpec supplies the
// canonical env-var name per key — so new providers get export support for
// free.
//
// Exports intentionally include secret values (that is the point of an
// export); callers are responsible for warning the user and for using
// concealed clipboard types.
package export

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ez-cloud-manager/internal/provider"
)

// Formats lists supported format identifiers, in menu order.
func Formats() []string { return []string{"env", "dotenv", "ini", "json"} }

// Render serializes fields per format. Field order: schema order first, then
// unknown keys sorted — stable output diffs cleanly across exports.
func Render(schema provider.Schema, name string, fields map[string]string, format string) (string, error) {
	ordered := orderedKeys(schema, fields)
	switch format {
	case "env":
		return renderEnv(schema, fields, ordered, true), nil
	case "dotenv":
		return renderEnv(schema, fields, ordered, false), nil
	case "ini":
		return renderINI(name, fields, ordered), nil
	case "json":
		return renderJSON(name, fields)
	default:
		return "", fmt.Errorf("unknown export format %q (supported: %s)", format, strings.Join(Formats(), ", "))
	}
}

func orderedKeys(schema provider.Schema, fields map[string]string) []string {
	inSchema := map[string]bool{}
	var ordered []string
	for _, spec := range schema.Fields {
		inSchema[spec.Key] = true
		if _, ok := fields[spec.Key]; ok {
			ordered = append(ordered, spec.Key)
		}
	}
	var extras []string
	for key := range fields {
		if !inSchema[key] {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}

func envName(schema provider.Schema, key string) string {
	for _, spec := range schema.Fields {
		if spec.Key == key {
			return spec.Env
		}
	}
	return ""
}

// renderEnv emits `export NAME='value'` (shell=true) or `NAME=value` dotenv
// lines. Keys without a canonical env var are emitted as comments so the
// export is complete but never invents variable names tools would ignore.
func renderEnv(schema provider.Schema, fields map[string]string, ordered []string, shell bool) string {
	var b strings.Builder
	for _, key := range ordered {
		value := fields[key]
		if value == "" {
			continue
		}
		env := envName(schema, key)
		if env == "" {
			fmt.Fprintf(&b, "# %s=%s (no standard environment variable)\n", key, value)
			continue
		}
		if shell {
			fmt.Fprintf(&b, "export %s=%s\n", env, shellQuote(value))
		} else {
			fmt.Fprintf(&b, "%s=%s\n", env, dotenvQuote(value))
		}
	}
	return b.String()
}

// renderINI emits a provider-native block. Dotted keys (gcloud
// "section.property") group under their section; flat keys go under the
// profile's own [name] section.
func renderINI(name string, fields map[string]string, ordered []string) string {
	type kv struct{ key, value string }
	flat := []kv{}
	bySection := map[string][]kv{}
	var sectionOrder []string
	for _, key := range ordered {
		value := fields[key]
		if value == "" {
			continue
		}
		if sec, prop, found := strings.Cut(key, "."); found && sec != "" && prop != "" {
			if _, seen := bySection[sec]; !seen {
				sectionOrder = append(sectionOrder, sec)
			}
			bySection[sec] = append(bySection[sec], kv{prop, value})
		} else {
			flat = append(flat, kv{key, value})
		}
	}

	var b strings.Builder
	if len(flat) > 0 {
		fmt.Fprintf(&b, "[%s]\n", name)
		for _, item := range flat {
			fmt.Fprintf(&b, "%s = %s\n", item.key, item.value)
		}
	}
	for _, sec := range sectionOrder {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s]\n", sec)
		for _, item := range bySection[sec] {
			fmt.Fprintf(&b, "%s = %s\n", item.key, item.value)
		}
	}
	return b.String()
}

func renderJSON(name string, fields map[string]string) (string, error) {
	payload := struct {
		Name   string            `json:"name"`
		Fields map[string]string `json:"fields"`
	}{Name: name, Fields: fields}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// shellQuote single-quotes for POSIX shells; embedded single quotes use the
// standard '\'' splice so pasted lines survive any value byte-for-byte.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// dotenvQuote leaves plain values bare and double-quotes anything a dotenv
// parser could misread (spaces, quotes, #, backslashes).
func dotenvQuote(value string) string {
	if !strings.ContainsAny(value, " \t\"'#\\") {
		return value
	}
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
