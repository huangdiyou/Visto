package storage

import (
	"context"
	"sort"
)

const (
	maxScanFiles = 100_000
	maxScanDepth = 64
)

type scanDirectory struct {
	key   string
	depth int
}

func scanRoot(
	ctx context.Context,
	root Adapter,
) ([]ObservedFile, error) {
	pending := []scanDirectory{{key: "", depth: 0}}
	observed := make([]ObservedFile, 0)

	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current.depth > maxScanDepth {
			return nil, ErrScanLimitExceeded
		}

		entries, err := root.List(ctx, current.key)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			switch entry.Kind {
			case "directory":
				pending = append(pending, scanDirectory{
					key:   entry.ObjectKey,
					depth: current.depth + 1,
				})
			case "file":
				file, err := root.Observe(ctx, entry.ObjectKey)
				if err != nil {
					return nil, err
				}
				observed = append(observed, file)
				if len(observed) > maxScanFiles {
					return nil, ErrScanLimitExceeded
				}
			}
		}
	}

	sort.Slice(observed, func(i, j int) bool {
		return observed[i].ObjectKey < observed[j].ObjectKey
	})
	return observed, nil
}
