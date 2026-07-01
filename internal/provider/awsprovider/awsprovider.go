// Package awsprovider adapts the AWS credentials backend (internal/awscreds)
// to the generic provider.Provider interface and registers it as "aws".
//
// Import for side effects to make the provider available:
//
//	import _ "ez-cloud-manager/internal/provider/awsprovider"
package awsprovider

import (
	"ez-cloud-manager/internal/awscreds"
	"ez-cloud-manager/internal/provider"
)

const id = "aws"

// awsProvider delegates every operation to the existing awscreds package,
// converting between its concrete types and the provider-agnostic DTOs.
type awsProvider struct{}

// New returns the AWS credentials provider, backed by ~/.aws/credentials.
func New() provider.Provider { return awsProvider{} }

func (awsProvider) ID() string          { return id }
func (awsProvider) DisplayName() string { return "AWS" }

func (awsProvider) DefaultPath() (string, error) { return awscreds.DefaultPath() }

func (awsProvider) List(path string) ([]provider.ProfileSummary, error) {
	summaries, err := awscreds.List(path)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ProfileSummary, len(summaries))
	for i, s := range summaries {
		out[i] = provider.ProfileSummary{Name: s.Name, Keys: s.Keys}
	}
	return out, nil
}

func (awsProvider) Get(path, name string) (provider.Profile, error) {
	p, err := awscreds.Get(path, name)
	if err != nil {
		return provider.Profile{}, err
	}
	return provider.Profile{Name: p.Name, Fields: p.Fields}, nil
}

func (awsProvider) Save(path, name string, fields map[string]string) error {
	return awscreds.Save(path, name, fields)
}

func (awsProvider) Delete(path, name string) error {
	return awscreds.Delete(path, name)
}

func (awsProvider) Parse(text string) provider.Parsed {
	p := awscreds.Parse(text)
	return provider.Parsed{ProfileName: p.ProfileName, Fields: p.Fields}
}

func init() { provider.Register(New()) }
