package httpapi

import (
	"fmt"
	"os"
	"strings"
)

// AllowWebHostPathsFromEnvironment reads the deployment override for the
// first-run host access choice (D2, docs/FREE_TIER_BOUNDARY_DESIGN.md).
//
// An unset variable returns nil so the value recorded by the wizard applies. An
// unparsable value is an error rather than a silent fallback: an operator who
// wrote a value here believes the boundary is pinned, and quietly ignoring the
// typo would leave the instance on the wizard's answer instead.
func AllowWebHostPathsFromEnvironment() (*bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("VISTO_ALLOW_WEB_HOST_PATHS")))
	switch value {
	case "":
		return nil, nil
	case "1", "true", "yes", "on":
		allowed := true
		return &allowed, nil
	case "0", "false", "no", "off":
		allowed := false
		return &allowed, nil
	default:
		return nil, fmt.Errorf(
			"VISTO_ALLOW_WEB_HOST_PATHS must be 1 or 0, got %q", value,
		)
	}
}
