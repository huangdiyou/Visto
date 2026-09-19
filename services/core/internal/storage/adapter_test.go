package storage

import (
	"context"
	"errors"
	"testing"
)

func TestProviderRegistryResolvesRegisteredAdapter(t *testing.T) {
	registry := NewProviderRegistry()
	expected := &LocalRoot{rootPath: "test-root"}
	if err := registry.Register("custom", func(
		_ context.Context,
		config AdapterConfig,
	) (Adapter, error) {
		if config.ProviderID != "provider-1" {
			t.Fatalf("unexpected provider config: %#v", config)
		}
		return expected, nil
	}); err != nil {
		t.Fatalf("register provider: %v", err)
	}
	adapter, err := registry.Open(context.Background(), AdapterConfig{
		ProviderID: "provider-1",
		Kind:       "custom",
	})
	if err != nil {
		t.Fatalf("open provider: %v", err)
	}
	if adapter != expected {
		t.Fatal("registry returned a different adapter")
	}
	if err := registry.Register("custom", func(
		context.Context,
		AdapterConfig,
	) (Adapter, error) {
		return expected, nil
	}); err == nil {
		t.Fatal("expected duplicate provider registration to fail")
	}
	if _, err := registry.Open(context.Background(), AdapterConfig{
		Kind: "missing",
	}); !errors.Is(err, ErrProviderUnsupported) {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}
