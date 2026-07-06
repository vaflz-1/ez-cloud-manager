package export

import (
	"strings"
	"testing"

	"ez-cloud-manager/internal/provider"
)

func testSchema() provider.Schema {
	return provider.Schema{
		Provider:    "test",
		DisplayName: "Test",
		Fields: []provider.FieldSpec{
			{Key: "access_key", Env: "TEST_ACCESS_KEY"},
			{Key: "secret_key", Env: "TEST_SECRET_KEY", Secret: true},
			{Key: "region", Env: "TEST_REGION"},
			{Key: "ini_only"},
		},
	}
}

func TestRenderEnvQuotingAndOrder(t *testing.T) {
	fields := map[string]string{
		"access_key": "AK",
		"secret_key": "it's;tricky",
		"region":     "us-east-1",
		"ini_only":   "local",
		"zz_extra":   "extra",
	}
	out, err := Render(testSchema(), "p", fields, "env")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `export TEST_SECRET_KEY='it'\''s;tricky'`) {
		t.Errorf("bad shell quoting:\n%s", out)
	}
	if !strings.Contains(out, "# ini_only=local") {
		t.Errorf("keys without env var must appear as comments:\n%s", out)
	}
	// Schema order before extras.
	if strings.Index(out, "TEST_ACCESS_KEY") > strings.Index(out, "zz_extra") {
		t.Errorf("schema keys must precede extras:\n%s", out)
	}
}

func TestRenderDotenv(t *testing.T) {
	out, err := Render(testSchema(), "p", map[string]string{
		"access_key": "plain",
		"secret_key": `has "quotes" and spaces`,
	}, "dotenv")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "TEST_ACCESS_KEY=plain\n") {
		t.Errorf("plain value must stay bare:\n%s", out)
	}
	if !strings.Contains(out, `TEST_SECRET_KEY="has \"quotes\" and spaces"`) {
		t.Errorf("bad dotenv quoting:\n%s", out)
	}
}

func TestRenderINIFlatAndDotted(t *testing.T) {
	out, err := Render(testSchema(), "myprofile", map[string]string{"access_key": "AK"}, "ini")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[myprofile]\naccess_key = AK") {
		t.Errorf("flat ini wrong:\n%s", out)
	}

	dotted, err := Render(provider.Schema{}, "work", map[string]string{
		"core.project":   "p",
		"compute.region": "r",
	}, "ini")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dotted, "[core]\nproject = p") || !strings.Contains(dotted, "[compute]\nregion = r") {
		t.Errorf("dotted keys must group into sections:\n%s", dotted)
	}
}

func TestRenderJSONAndUnknownFormat(t *testing.T) {
	out, err := Render(testSchema(), "p", map[string]string{"region": "r"}, "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name": "p"`) || !strings.Contains(out, `"region": "r"`) {
		t.Errorf("json wrong:\n%s", out)
	}
	if _, err := Render(testSchema(), "p", nil, "yaml"); err == nil {
		t.Error("unknown format must error")
	}
}

func TestEmptyValuesSkipped(t *testing.T) {
	out, err := Render(testSchema(), "p", map[string]string{"region": ""}, "env")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "TEST_REGION") {
		t.Errorf("empty values must be skipped:\n%s", out)
	}
}
