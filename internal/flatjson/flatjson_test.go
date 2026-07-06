package flatjson

import (
	"encoding/json"
	"reflect"
	"testing"
)

// mustDecode parses JSON the same way the app does at runtime, so tests operate
// on the map[string]any / []any / float64 shapes Unflatten is built around.
func mustDecode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func TestFlatten(t *testing.T) {
	src := mustDecode(t, `{
		"InstanceType": "t2.micro",
		"Count": 3,
		"Ratio": 3.5,
		"EbsOptimized": true,
		"KmsKeyId": null,
		"SecurityGroupIds": ["sg-1", "sg-2"],
		"Placement": {"Tenancy": "default"}
	}`)

	got := Flatten(src)
	want := map[string]string{
		"InstanceType":        "t2.micro",
		"Count":               "3",   // integral number renders without a fraction
		"Ratio":               "3.5", // fractional number keeps its digits
		"EbsOptimized":        "true",
		"KmsKeyId":            "", // null renders empty
		"SecurityGroupIds[0]": "sg-1",
		"SecurityGroupIds[1]": "sg-2",
		"Placement.Tenancy":   "default",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Flatten mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestFlattenEmptyContainers(t *testing.T) {
	// Empty maps and arrays have no addressable leaves and contribute nothing.
	got := Flatten(mustDecode(t, `{"A": {}, "B": [], "C": "x"}`))
	want := map[string]string{"C": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRoundTripIdentity(t *testing.T) {
	// A document with every leaf kind except empty-string leaves (which the
	// removal-on-empty rule cannot preserve by design) must survive a full
	// Flatten -> Unflatten cycle unchanged.
	src := mustDecode(t, `{
		"InstanceType": "t3.large",
		"CpuOptions": {"CoreCount": 2, "ThreadsPerCore": 1},
		"Monitoring": {"Enabled": false},
		"KmsKeyId": null,
		"Tags": ["prod", "web"],
		"Nested": [{"Value": 10.25, "On": true}]
	}`)

	flat := Flatten(src)
	got, err := Unflatten(src, flat)
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	if !reflect.DeepEqual(got, src) {
		t.Fatalf("round-trip changed the document\n got: %#v\nwant: %#v", got, src)
	}
}

func TestUnflattenTypePreservation(t *testing.T) {
	src := mustDecode(t, `{"Port": 8080, "Enabled": true, "Name": "web"}`)
	got, err := Unflatten(src, map[string]string{
		"Port":    "9090",
		"Enabled": "false",
		"Name":    "api",
	})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	m := got.(map[string]any)

	if n, ok := m["Port"].(float64); !ok || n != 9090 {
		t.Fatalf("Port = %#v, want float64(9090)", m["Port"])
	}
	if b, ok := m["Enabled"].(bool); !ok || b != false {
		t.Fatalf("Enabled = %#v, want bool(false)", m["Enabled"])
	}
	if s, ok := m["Name"].(string); !ok || s != "api" {
		t.Fatalf("Name = %#v, want string(api)", m["Name"])
	}
}

func TestUnflattenNewKeyIsString(t *testing.T) {
	// A brand-new key that looks numeric must stay a string: launch-template
	// identifiers like "8080" must not be silently converted to numbers.
	src := mustDecode(t, `{"InstanceType": "t2.micro"}`)
	got, err := Unflatten(src, map[string]string{"UserPort": "8080"})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	m := got.(map[string]any)
	if s, ok := m["UserPort"].(string); !ok || s != "8080" {
		t.Fatalf("UserPort = %#v, want string(8080)", m["UserPort"])
	}
}

func TestUnflattenNewBool(t *testing.T) {
	src := mustDecode(t, `{"InstanceType": "t2.micro"}`)
	got, err := Unflatten(src, map[string]string{"DisableApiStop": "true"})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	m := got.(map[string]any)
	if b, ok := m["DisableApiStop"].(bool); !ok || b != true {
		t.Fatalf("DisableApiStop = %#v, want bool(true)", m["DisableApiStop"])
	}
}

func TestUnflattenNewNestedPath(t *testing.T) {
	// Intermediate object and array are created on demand.
	src := mustDecode(t, `{}`)
	got, err := Unflatten(src, map[string]string{"Net[0].DeviceIndex": "eth0"})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	want := mustDecode(t, `{"Net": [{"DeviceIndex": "eth0"}]}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestUnflattenArrayAppend(t *testing.T) {
	src := mustDecode(t, `{"Tags": ["a"]}`)
	got, err := Unflatten(src, map[string]string{
		"Tags[1]": "b",
		"Tags[2]": "c",
	})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	tags := got.(map[string]any)["Tags"].([]any)
	if len(tags) != 3 || tags[0] != "a" || tags[1] != "b" || tags[2] != "c" {
		t.Fatalf("Tags = %#v, want [a b c]", tags)
	}
}

func TestUnflattenErrors(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		edits map[string]string
	}{
		{"array index out of range", `{"Tags": ["a"]}`, map[string]string{"Tags[5]": "z"}},
		{"bad bool coercion", `{"Enabled": true}`, map[string]string{"Enabled": "yes"}},
		{"bad number coercion", `{"Port": 8080}`, map[string]string{"Port": "abc"}},
		{"unterminated bracket", `{}`, map[string]string{"Tags[0": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := mustDecode(t, tt.src)
			if _, err := Unflatten(src, tt.edits); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestUnflattenRemovalViaEmptyString(t *testing.T) {
	// Removing a map key drops it entirely; removing an array element nulls the
	// slot so sibling indices stay stable.
	src := mustDecode(t, `{"Keep": "y", "Drop": "x", "List": ["a", "b"]}`)
	got, err := Unflatten(src, map[string]string{
		"Drop":    "",
		"List[0]": "",
	})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	m := got.(map[string]any)
	if _, present := m["Drop"]; present {
		t.Fatalf("Drop should have been removed, got %#v", m["Drop"])
	}
	if m["Keep"] != "y" {
		t.Fatalf("Keep = %#v, want y", m["Keep"])
	}
	list := m["List"].([]any)
	if len(list) != 2 || list[0] != nil || list[1] != "b" {
		t.Fatalf("List = %#v, want [null b]", list)
	}
}

func TestUnflattenNullHandling(t *testing.T) {
	src := mustDecode(t, `{"KmsKeyId": null}`)

	// Empty edit keeps a null as null.
	got, err := Unflatten(src, map[string]string{"KmsKeyId": ""})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	if v := got.(map[string]any)["KmsKeyId"]; v != nil {
		t.Fatalf("KmsKeyId = %#v, want nil", v)
	}

	// A non-empty edit turns a null into that string.
	got, err = Unflatten(src, map[string]string{"KmsKeyId": "key-123"})
	if err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	if v := got.(map[string]any)["KmsKeyId"]; v != "key-123" {
		t.Fatalf("KmsKeyId = %#v, want key-123", v)
	}
}

func TestUnflattenDoesNotMutateSource(t *testing.T) {
	src := mustDecode(t, `{"InstanceType": "t2.micro", "Tags": ["a"]}`)
	before := mustDecode(t, `{"InstanceType": "t2.micro", "Tags": ["a"]}`)
	if _, err := Unflatten(src, map[string]string{"InstanceType": "t3.large", "Tags[1]": "b"}); err != nil {
		t.Fatalf("Unflatten: %v", err)
	}
	if !reflect.DeepEqual(src, before) {
		t.Fatalf("source mutated: %#v", src)
	}
}
