package serverupdate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Downgrade guard for the free Server tier, per
// docs/FREE_TIER_BOUNDARY_DESIGN.md (D1).
//
// The free tier reads an unsigned version channel, so the client is the only
// place that can refuse to walk backwards. The rule is deliberately
// conservative:
//
//   - an update is offered only when the published version is STRICTLY greater
//     than the running version;
//   - anything that cannot be compared (empty, malformed, unparsable, or an
//     unparsable running version such as a development build) is treated as
//     "no update", never as an upgrade.
//
// Explicit operator actions are deliberately outside this comparison: running
// the update script against a chosen package, and the rollback command, are
// intentional and must keep working.

// Version is a parsed release version: up to three numeric components plus an
// optional pre-release identifier list. Build metadata is ignored, matching
// SemVer precedence rules.
type Version struct {
	numbers [3]int
	pre     []string
}

// ParseVersion accepts an optional leading "v", one to three dot-separated
// numeric components, and an optional "-pre.release" suffix. Build metadata
// after "+" is ignored.
func ParseVersion(value string) (Version, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return Version{}, errors.New("version is empty")
	}
	if raw[0] == 'v' || raw[0] == 'V' {
		raw = raw[1:]
		if raw == "" {
			return Version{}, fmt.Errorf("version %q has no numeric components", value)
		}
	}
	if index := strings.IndexByte(raw, '+'); index >= 0 {
		raw = raw[:index]
	}
	core := raw
	preRelease := ""
	hasPreRelease := false
	if index := strings.IndexByte(raw, '-'); index >= 0 {
		core = raw[:index]
		preRelease = raw[index+1:]
		hasPreRelease = true
	}

	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, fmt.Errorf("version %q must have one to three numeric components", value)
	}
	parsed := Version{}
	for index, part := range parts {
		if !isDigits(part) {
			return Version{}, fmt.Errorf("version %q has a non-numeric component", value)
		}
		number, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("version %q has an out-of-range component", value)
		}
		parsed.numbers[index] = number
	}
	if hasPreRelease {
		if preRelease == "" {
			return Version{}, fmt.Errorf("version %q has an empty pre-release", value)
		}
		parsed.pre = strings.Split(preRelease, ".")
		for _, identifier := range parsed.pre {
			if identifier == "" {
				return Version{}, fmt.Errorf("version %q has an empty pre-release identifier", value)
			}
		}
	}
	return parsed, nil
}

// Compare returns -1, 0 or 1 when v is lower than, equal to, or higher than
// other.
func (v Version) Compare(other Version) int {
	for index := range v.numbers {
		if v.numbers[index] == other.numbers[index] {
			continue
		}
		if v.numbers[index] < other.numbers[index] {
			return -1
		}
		return 1
	}
	// A pre-release always sorts below the release carrying the same numbers.
	switch {
	case len(v.pre) == 0 && len(other.pre) == 0:
		return 0
	case len(v.pre) == 0:
		return 1
	case len(other.pre) == 0:
		return -1
	}
	for index := 0; index < len(v.pre) && index < len(other.pre); index++ {
		if result := comparePreReleaseIdentifier(v.pre[index], other.pre[index]); result != 0 {
			return result
		}
	}
	switch {
	case len(v.pre) < len(other.pre):
		return -1
	case len(v.pre) > len(other.pre):
		return 1
	}
	return 0
}

// CompareVersions returns -1, 0 or 1 for left lower than, equal to, or higher
// than right. An error means at least one side could not be parsed.
func CompareVersions(left, right string) (int, error) {
	parsedLeft, err := ParseVersion(left)
	if err != nil {
		return 0, err
	}
	parsedRight, err := ParseVersion(right)
	if err != nil {
		return 0, err
	}
	return parsedLeft.Compare(parsedRight), nil
}

// IsUpgrade reports whether latest should be offered to an installation running
// current. Any comparison failure yields false, so an unreadable or malformed
// version can never be presented as an available update.
func IsUpgrade(current, latest string) bool {
	comparison, err := CompareVersions(current, latest)
	return err == nil && comparison < 0
}

func comparePreReleaseIdentifier(left, right string) int {
	leftNumeric := isDigits(left)
	rightNumeric := isDigits(right)
	switch {
	case leftNumeric && rightNumeric:
		leftNumber, _ := strconv.Atoi(left)
		rightNumber, _ := strconv.Atoi(right)
		switch {
		case leftNumber == rightNumber:
			return 0
		case leftNumber < rightNumber:
			return -1
		}
		return 1
	case leftNumeric:
		// Numeric identifiers always have lower precedence than alphanumeric.
		return -1
	case rightNumeric:
		return 1
	}
	return strings.Compare(left, right)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
