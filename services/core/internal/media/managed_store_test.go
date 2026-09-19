package media

import (
	"os"
	"testing"
)

func TestManagedStoreRejectsEscapingObjectKey(t *testing.T) {
	store, err := NewManagedStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	for _, key := range []string{"../escape.webp", "/absolute.webp", `a\b.webp`} {
		if err := store.Write(key, []byte("image")); err == nil {
			t.Fatalf("expected key %q to be rejected", key)
		}
	}
}

func TestManagedStorePublishesAtomically(t *testing.T) {
	store, err := NewManagedStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.Write("workspace/object/preview.webp", []byte("first")); err != nil {
		t.Fatalf("write first rendition: %v", err)
	}
	if err := store.Write("workspace/object/preview.webp", []byte("second")); err != nil {
		t.Fatalf("replace rendition: %v", err)
	}
	file, _, err := store.Open("workspace/object/preview.webp")
	if err != nil {
		t.Fatalf("open rendition: %v", err)
	}
	defer file.Close()
	value, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("read rendition: %v", err)
	}
	if string(value) != "second" {
		t.Fatalf("unexpected rendition content %q", value)
	}
}
