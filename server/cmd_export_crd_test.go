package main

import (
	"strings"
	"testing"
)

// TestValidateCRDNamespace pins the namespace guard added for review feedback:
// --namespace comes straight from user input and flows into generated
// AlertmanagerConfig/Secret manifests, so invalid values must be rejected
// rather than silently coerced.
func TestValidateCRDNamespace(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"simple", "monitoring", false},
		{"with dashes", "team-alerting-prod", false},
		{"single char", "a", false},
		{"digits", "ns1", false},
		{"empty", "", true},
		{"uppercase", "Monitoring", true},
		{"leading dash", "-monitoring", true},
		{"trailing dash", "monitoring-", true},
		{"underscore", "mon_itoring", true},
		{"slash (yaml/path injection)", "monitoring/evil", true},
		{"whitespace", "mon itoring", true},
		{"newline injection", "monitoring\nfoo: bar", true},
		{"max length ok (63)", strings.Repeat("a", 63), false},
		{"too long (64)", strings.Repeat("a", 64), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCRDNamespace(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateCRDNamespace(%q) error = %v, wantErr = %v", tc.in, err, tc.wantErr)
			}
		})
	}
}
