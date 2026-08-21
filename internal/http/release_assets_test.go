package http

import "testing"

func TestReleaseAssetHostAllowed(t *testing.T) {
	cases := []struct {
		provider, host string
		want           bool
	}{
		{"gh", "github.com", true},
		{"gh", "objects.githubusercontent.com", true},
		{"gh", "release-assets.githubusercontent.com", true},
		{"gh", "evil.githubusercontent.com.evil.io", false},
		{"gh", "gitlab.com", false},
		{"gl", "gitlab.com", true},
		{"cb", "codeberg.org", true},
		{"cb", "github.com", false},
	}
	for _, tc := range cases {
		if got := releaseAssetHostAllowed(tc.provider, tc.host); got != tc.want {
			t.Errorf("releaseAssetHostAllowed(%q, %q) = %v, want %v", tc.provider, tc.host, got, tc.want)
		}
	}
}
