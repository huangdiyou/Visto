package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

type ObjectReader interface {
	io.Reader
	io.Closer
}

type Adapter interface {
	Kind() string
	Capabilities() map[string]bool
	Stat(context.Context, string) (FileInfo, error)
	List(context.Context, string) ([]DirectoryEntry, error)
	OpenRange(context.Context, string, ByteRange) (ObjectReader, FileInfo, error)
	Observe(context.Context, string) (ObservedFile, error)
	Put(context.Context, string, io.Reader, int64, string) (FileInfo, error)
	Move(context.Context, string, string) error
	Copy(context.Context, string, string) error
	Delete(context.Context, string) error
}

type AdapterConfig struct {
	WorkspaceID string
	ProviderID  string
	RootID      string
	Kind        string
	LocalPath   string
	Mode        string
	Config      map[string]string
	Secret      []byte
}

type AdapterFactory func(context.Context, AdapterConfig) (Adapter, error)

type ProviderRegistry struct {
	mu        sync.RWMutex
	factories map[string]AdapterFactory
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{factories: make(map[string]AdapterFactory)}
}

func NewDefaultProviderRegistry() *ProviderRegistry {
	registry := NewProviderRegistry()
	_ = registry.Register("local", func(
		_ context.Context,
		config AdapterConfig,
	) (Adapter, error) {
		return newLocalRoot(config.LocalPath, config.Mode == "managed")
	})
	_ = registry.Register("webdav", func(
		ctx context.Context,
		config AdapterConfig,
	) (Adapter, error) {
		return NewWebDAVAdapter(ctx, config)
	})
	_ = registry.Register("s3", func(
		ctx context.Context,
		config AdapterConfig,
	) (Adapter, error) {
		return NewS3Adapter(ctx, config)
	})
	return registry
}

func (registry *ProviderRegistry) Register(
	kind string,
	factory AdapterFactory,
) error {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || factory == nil {
		return fmt.Errorf("%w: provider registration is invalid", ErrInvalidRootInput)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.factories[kind]; exists {
		return fmt.Errorf("%w: provider %q is already registered", ErrInvalidRootInput, kind)
	}
	registry.factories[kind] = factory
	return nil
}

func (registry *ProviderRegistry) Open(
	ctx context.Context,
	config AdapterConfig,
) (Adapter, error) {
	kind := strings.ToLower(strings.TrimSpace(config.Kind))
	registry.mu.RLock()
	factory := registry.factories[kind]
	registry.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderUnsupported, kind)
	}
	return factory(ctx, config)
}

func (registry *ProviderRegistry) Kinds() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]string, 0, len(registry.factories))
	for kind := range registry.factories {
		result = append(result, kind)
	}
	return result
}
