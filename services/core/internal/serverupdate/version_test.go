package serverupdate

import "testing"

func TestCompareVersionsOrdersReleases(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		want  int
	}{
		{"identical", "1.0.0", "1.0.0", 0},
		{"leading v is ignored", "v1.0.0", "1.0.0", 0},
		{"missing components default to zero", "1.0", "1.0.0", 0},
		{"single component", "1", "1.0.0", 0},
		{"build metadata has no precedence", "1.0.0+build.5", "1.0.0", 0},
		{"patch bump", "1.0.0", "1.0.1", -1},
		{"numeric not lexical", "1.0.9", "1.0.10", -1},
		{"minor bump", "1.10.0", "1.9.0", 1},
		{"major bump", "2.0.0", "1.9.9", 1},
		{"pre-release sorts below release", "1.0.0-rc.1", "1.0.0", -1},
		{"pre-release order", "1.0.0-rc.1", "1.0.0-rc.2", -1},
		{"alphanumeric pre-release order", "1.0.0-alpha", "1.0.0-beta", -1},
		{"longer pre-release wins on shared prefix", "1.0.0-alpha.1", "1.0.0-alpha", 1},
		{"numeric pre-release below alphanumeric", "1.0.0-1", "1.0.0-alpha", -1},
		{"numeric pre-release identifiers compare numerically", "1.0.0-2", "1.0.0-10", -1},
		// Regression for the free tier decision: a channel that advertises an
		// older release than the one running must never look like an upgrade.
		{"installed integration build is ahead of 1.0.0", "1.0.3-integration.3", "1.0.0", 1},
		{"release candidate is behind the stable release", "1.0.0-rc.1", "1.0.0", -1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := CompareVersions(testCase.left, testCase.right)
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q) returned error: %v", testCase.left, testCase.right, err)
			}
			if got != testCase.want {
				t.Fatalf("CompareVersions(%q, %q) = %d, want %d", testCase.left, testCase.right, got, testCase.want)
			}
		})
	}
}

func TestCompareVersionsRejectsUnparsableInput(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
	}{
		{"empty left", "", "1.0.0"},
		{"empty right", "1.0.0", ""},
		{"non numeric", "abc", "1.0.0"},
		{"empty pre-release", "1.0.0-", "1.0.0"},
		{"empty component", "1..0", "1.0.0"},
		{"too many components", "1.0.0.0", "1.0.0"},
		{"dangling v", "v", "1.0.0"},
		{"empty pre-release identifier", "1.0.0-alpha..1", "1.0.0"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := CompareVersions(testCase.left, testCase.right); err == nil {
				t.Fatalf("CompareVersions(%q, %q) unexpectedly succeeded", testCase.left, testCase.right)
			}
		})
	}
}

func TestIsUpgradeOnlyAcceptsStrictlyNewerVersions(t *testing.T) {
	cases := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{"newer patch", "1.0.0", "1.0.1", true},
		{"newer with leading v", "1.0.0", "v1.0.1", true},
		{"same version", "1.0.0", "1.0.0", false},
		{"stable is not an upgrade over its own release candidate", "1.0.0-rc.1", "1.0.0-rc.1", false},
		{"downgrade is refused", "1.0.3-integration.3", "1.0.0", false},
		{"downgrade across pre-release ranks is refused", "1.0.0", "1.0.0-rc.1", false},
		{"malformed latest is refused", "1.0.0", "not-a-version", false},
		{"empty latest is refused", "1.0.0", "", false},
		{"unparsable running version is refused", "development", "1.0.1", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsUpgrade(testCase.current, testCase.latest); got != testCase.want {
				t.Fatalf("IsUpgrade(%q, %q) = %v, want %v", testCase.current, testCase.latest, got, testCase.want)
			}
		})
	}
}
