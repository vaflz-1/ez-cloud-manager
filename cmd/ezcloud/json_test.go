package main

import (
	"strings"
	"testing"
)

func TestDecodeLimitedJSONIsStrict(t *testing.T) {
	for name, input := range map[string]string{
		"unknown field":   `{"fields":{},"unexpected":true}`,
		"multiple values": `{"fields":{}} {"fields":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var request saveRequest
			if err := decodeLimitedJSON(strings.NewReader(input), &request); err == nil {
				t.Fatalf("decodeLimitedJSON(%q) unexpectedly succeeded", input)
			}
		})
	}
}

func TestDecodeLimitedJSONAcceptsEmptyExpectedFields(t *testing.T) {
	var request saveRequest
	input := `{"fields":{"region":"eu-west-1"},"expectedFields":{},"expectAbsent":false}`
	if err := decodeLimitedJSON(strings.NewReader(input), &request); err != nil {
		t.Fatal(err)
	}
	if request.ExpectedFields == nil || len(request.ExpectedFields) != 0 {
		t.Fatalf("empty optimistic baseline was not preserved: %+v", request)
	}
}
