package main

import "testing"

func TestOnlyAnOlderBuildIsRefused(t *testing.T) {
	defer func(v string) { version = v }(version)
	for _, tc := range []struct {
		self, installed string
		want            bool
	}{
		{"20261027.231100-e68249f", "20261028.090000-aaaaaaa", true},
		{"20261027.231100-e68249f", "20261027.231100-e68249f", false}, // the same again repairs
		{"20261027.231100-e68249f", "20261020.120000-bbbbbbb", false}, // an update
		{"20261027.231100-e68249f", "", false},                        // installed before versions
		{"", "20261028.090000-aaaaaaa", false},                        // a development build
	} {
		version = tc.self
		if got := olderThan(tc.installed); got != tc.want {
			t.Errorf("%s over %q: older %v, want %v", tc.self, tc.installed, got, tc.want)
		}
	}
}
