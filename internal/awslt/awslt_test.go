package awslt

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeRunner records every invocation and delegates to fn for the response, so
// tests can assert the exact argv the Client builds and feed canned AWS output.
type fakeRunner struct {
	calls  [][]string
	stdins [][]byte
	fn     func(args []string, stdin []byte) ([]byte, error)
}

func (f *fakeRunner) run(args []string, stdin []byte) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	f.stdins = append(f.stdins, stdin)
	return f.fn(args, stdin)
}

func testClient(fn func(args []string, stdin []byte) ([]byte, error)) (Client, *fakeRunner) {
	f := &fakeRunner{fn: fn}
	return Client{Profile: "prod", Region: "us-east-1", Run: f.run}, f
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestListTemplates(t *testing.T) {
	resp := `{
		"LaunchTemplates": [
			{
				"LaunchTemplateId": "lt-0abc",
				"LaunchTemplateName": "web",
				"CreateTime": "2024-01-02T03:04:05+00:00",
				"CreatedBy": "arn:aws:iam::123456789012:user/dev",
				"DefaultVersionNumber": 1,
				"LatestVersionNumber": 4
			}
		]
	}`
	c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
		return []byte(resp), nil
	})

	got, err := c.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	assertArgs(t, f.calls[0], []string{
		"ec2", "describe-launch-templates",
		"--profile", "prod", "--region", "us-east-1",
		"--output", "json", "--no-cli-pager",
	})
	want := []Template{{
		ID: "lt-0abc", Name: "web", DefaultVersion: 1, LatestVersion: 4,
		CreatedBy: "arn:aws:iam::123456789012:user/dev", CreateTime: "2024-01-02T03:04:05+00:00",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("templates mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestListVersions(t *testing.T) {
	resp := `{
		"LaunchTemplateVersions": [
			{"VersionNumber": 1, "VersionDescription": "initial", "DefaultVersion": true, "CreateTime": "2024-01-01T00:00:00+00:00"},
			{"VersionNumber": 2, "VersionDescription": "bump ami", "DefaultVersion": false, "CreateTime": "2024-02-01T00:00:00+00:00"}
		]
	}`
	c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
		return []byte(resp), nil
	})

	got, err := c.ListVersions("web")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	assertArgs(t, f.calls[0], []string{
		"ec2", "describe-launch-template-versions",
		"--launch-template-name", "web",
		"--profile", "prod", "--region", "us-east-1",
		"--output", "json", "--no-cli-pager",
	})
	want := []Version{
		{Number: 1, Description: "initial", IsDefault: true, CreateTime: "2024-01-01T00:00:00+00:00"},
		{Number: 2, Description: "bump ami", IsDefault: false, CreateTime: "2024-02-01T00:00:00+00:00"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("versions mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestGetVersionData(t *testing.T) {
	resp := `{
		"LaunchTemplateVersions": [
			{"LaunchTemplateData": {"InstanceType": "t2.micro", "CpuOptions": {"CoreCount": 2}}}
		]
	}`
	c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
		return []byte(resp), nil
	})

	raw, flat, err := c.GetVersionData("web", "2")
	if err != nil {
		t.Fatalf("GetVersionData: %v", err)
	}
	assertArgs(t, f.calls[0], []string{
		"ec2", "describe-launch-template-versions",
		"--launch-template-name", "web", "--versions", "2",
		"--profile", "prod", "--region", "us-east-1",
		"--output", "json", "--no-cli-pager",
	})
	if flat["InstanceType"] != "t2.micro" || flat["CpuOptions.CoreCount"] != "2" {
		t.Fatalf("flat mismatch: %#v", flat)
	}
	// raw must be the LaunchTemplateData object, re-decodable on its own.
	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("raw not valid JSON object: %v", err)
	}
	if check["InstanceType"] != "t2.micro" {
		t.Fatalf("raw mismatch: %s", raw)
	}
}

func TestCreateVersionEndToEnd(t *testing.T) {
	source := `{
		"LaunchTemplateVersions": [
			{"LaunchTemplateData": {"InstanceType": "t2.micro", "CpuOptions": {"CoreCount": 2, "ThreadsPerCore": 1}}}
		]
	}`
	var wroteData map[string]any
	c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
		switch args[1] {
		case "describe-launch-template-versions":
			return []byte(source), nil
		case "create-launch-template-version":
			path := fileArg(t, args)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read temp data file: %v", err)
			}
			if err := json.Unmarshal(body, &wroteData); err != nil {
				t.Fatalf("temp data not JSON: %v", err)
			}
			return []byte(`{"LaunchTemplateVersion": {"VersionNumber": 5}}`), nil
		default:
			t.Fatalf("unexpected subcommand %q", args[1])
			return nil, nil
		}
	})

	newVersion, err := c.CreateVersion("web", "1", map[string]string{
		"InstanceType":         "t3.large", // string edit
		"CpuOptions.CoreCount": "4",        // numeric edit — must stay a number
	}, "bigger box")
	if err != nil {
		t.Fatalf("CreateVersion: %v", err)
	}
	if newVersion != 5 {
		t.Fatalf("newVersion = %d, want 5", newVersion)
	}

	// The edited data written to the temp file preserves types.
	if wroteData["InstanceType"] != "t3.large" {
		t.Fatalf("InstanceType = %#v, want t3.large", wroteData["InstanceType"])
	}
	cpu := wroteData["CpuOptions"].(map[string]any)
	if n, ok := cpu["CoreCount"].(float64); !ok || n != 4 {
		t.Fatalf("CoreCount = %#v, want float64(4)", cpu["CoreCount"])
	}
	if n, ok := cpu["ThreadsPerCore"].(float64); !ok || n != 1 {
		t.Fatalf("ThreadsPerCore = %#v, want float64(1) (untouched)", cpu["ThreadsPerCore"])
	}

	// Second call is the create, and the data goes via file:// (never argv).
	create := f.calls[1]
	dataIdx := indexOf(create, "--launch-template-data")
	if dataIdx < 0 || !strings.HasPrefix(create[dataIdx+1], "file://") {
		t.Fatalf("expected file:// data arg, got %q", create)
	}
	create[dataIdx+1] = "file://TMP"
	assertArgs(t, create, []string{
		"ec2", "create-launch-template-version",
		"--launch-template-name", "web",
		"--source-version", "1",
		"--version-description", "bigger box",
		"--launch-template-data", "file://TMP",
		"--profile", "prod", "--region", "us-east-1",
		"--output", "json", "--no-cli-pager",
	})
	// Temp file must be cleaned up.
	if _, err := os.Stat(strings.TrimPrefix(fileArgValue(f.calls[1]), "file://")); err == nil {
		t.Fatalf("temp data file was not removed")
	}
}

func TestSetDefaultVersion(t *testing.T) {
	c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
		return []byte(`{"LaunchTemplate": {"DefaultVersionNumber": 3}}`), nil
	})
	if err := c.SetDefaultVersion("web", "3"); err != nil {
		t.Fatalf("SetDefaultVersion: %v", err)
	}
	assertArgs(t, f.calls[0], []string{
		"ec2", "modify-launch-template",
		"--launch-template-name", "web", "--default-version", "3",
		"--profile", "prod", "--region", "us-east-1",
		"--output", "json", "--no-cli-pager",
	})
}

func TestDeleteVersions(t *testing.T) {
	c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
		return []byte(`{"SuccessfullyDeletedLaunchTemplateVersions": [{"VersionNumber": 2}, {"VersionNumber": 3}]}`), nil
	})
	if err := c.DeleteVersions("web", []string{"2", "3"}); err != nil {
		t.Fatalf("DeleteVersions: %v", err)
	}
	assertArgs(t, f.calls[0], []string{
		"ec2", "delete-launch-template-versions",
		"--launch-template-name", "web", "--versions", "2", "3",
		"--profile", "prod", "--region", "us-east-1",
		"--output", "json", "--no-cli-pager",
	})
}

func TestDeleteVersionsUnsuccessful(t *testing.T) {
	resp := `{
		"SuccessfullyDeletedLaunchTemplateVersions": [{"VersionNumber": 2}],
		"UnsuccessfullyDeletedLaunchTemplateVersions": [
			{"VersionNumber": 3, "ResponseError": {"Code": "LaunchTemplateVersionCurrentlyInUse", "Message": "Version 3 is the default"}}
		]
	}`
	c, _ := testClient(func(args []string, _ []byte) ([]byte, error) {
		return []byte(resp), nil
	})
	err := c.DeleteVersions("web", []string{"2", "3"})
	if err == nil {
		t.Fatal("expected error for unsuccessful deletion")
	}
	if !strings.Contains(err.Error(), "v3") || !strings.Contains(err.Error(), "is the default") {
		t.Fatalf("error should name the version and reason, got: %v", err)
	}
}

func TestRunnerErrorSurfaced(t *testing.T) {
	// The CLI's stderr (delivered by the Runner as an error) must propagate.
	c, _ := testClient(func(args []string, _ []byte) ([]byte, error) {
		return nil, errors.New("An error occurred (InvalidLaunchTemplateName.NotFound) when calling ...")
	})
	_, err := c.ListTemplates()
	if err == nil || !strings.Contains(err.Error(), "InvalidLaunchTemplateName.NotFound") {
		t.Fatalf("expected CLI error to surface, got: %v", err)
	}
}

func TestValidationRejectsBadInputBeforeExec(t *testing.T) {
	tests := []struct {
		name string
		call func(c Client) error
	}{
		{"bad version", func(c Client) error { _, _, e := c.GetVersionData("web", "abc"); return e }},
		{"bad delete version", func(c Client) error { return c.DeleteVersions("web", []string{"2", "$Nope"}) }},
		{"whitespace template name", func(c Client) error { _, e := c.ListVersions("bad name"); return e }},
		{"empty template name", func(c Client) error { _, e := c.ListVersions(""); return e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, f := testClient(func(args []string, _ []byte) ([]byte, error) {
				t.Fatalf("runner must not be called for invalid input: %q", args)
				return nil, nil
			})
			if err := tt.call(c); err == nil {
				t.Fatal("expected validation error")
			}
			if len(f.calls) != 0 {
				t.Fatalf("runner was invoked %d time(s) despite invalid input", len(f.calls))
			}
		})
	}
}

func TestValidationRejectsBadProfileRegion(t *testing.T) {
	c := Client{Profile: "", Region: "us-east-1", Run: func([]string, []byte) ([]byte, error) {
		t.Fatal("runner must not run with empty profile")
		return nil, nil
	}}
	if _, err := c.ListTemplates(); err == nil {
		t.Fatal("expected error for empty profile")
	}

	c = Client{Profile: "prod", Region: "us east 1", Run: func([]string, []byte) ([]byte, error) {
		t.Fatal("runner must not run with unsafe region")
		return nil, nil
	}}
	if _, err := c.ListTemplates(); err == nil {
		t.Fatal("expected error for unsafe region")
	}
}

func TestRunAWSStderrAndSuccess(t *testing.T) {
	// runAWS is the executor behind defaultRunner; exercise it with /bin/sh so
	// the stderr-surfacing and success paths get real coverage without aws.
	if _, err := runAWS("/bin/sh", []string{"-c", "echo boom 1>&2; exit 3"}, nil); err == nil {
		t.Fatal("expected error from non-zero exit")
	} else if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("stderr not surfaced, got: %v", err)
	}
	out, err := runAWS("/bin/sh", []string{"-c", "printf hello"}, nil)
	if err != nil {
		t.Fatalf("runAWS success: %v", err)
	}
	if string(out) != "hello" {
		t.Fatalf("stdout = %q, want hello", out)
	}
}

func TestCLINotFound(t *testing.T) {
	// Force the "aws not installed" path deterministically. awsOnce is reset to
	// a fresh Once (not copied from the old value, which would trip copylocks).
	savedLook := lookAWS
	defer func() {
		lookAWS = savedLook
		awsOnce = sync.Once{}
	}()
	awsOnce = sync.Once{}
	lookAWS = func() (string, error) { return "", errors.New("not found") }

	if CLIAvailable() {
		t.Fatal("CLIAvailable should be false when lookup fails")
	}
	_, err := defaultRunner([]string{"ec2", "describe-launch-templates"}, nil)
	if err == nil || !strings.Contains(err.Error(), "AWS CLI (aws) not found") {
		t.Fatalf("expected not-found error, got: %v", err)
	}
}

// fileArg returns the value following --launch-template-data, failing the test
// if it is missing.
func fileArg(t *testing.T, args []string) string {
	t.Helper()
	v := fileArgValue(args)
	if v == "" {
		t.Fatalf("no --launch-template-data in %q", args)
	}
	return strings.TrimPrefix(v, "file://")
}

func fileArgValue(args []string) string {
	i := indexOf(args, "--launch-template-data")
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func indexOf(args []string, flag string) int {
	for i, a := range args {
		if a == flag {
			return i
		}
	}
	return -1
}
