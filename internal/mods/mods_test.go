package mods

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestToggleAndOverlay(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("fake jar content")
	if err := os.WriteFile(filepath.Join(Dir(root), "a.jar"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Enabled || list[0].Name != "a.jar" {
		t.Fatalf("unexpected list: %+v", list)
	}

	if err := SetEnabled(root, "a.jar", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), "a.jar.disabled")); err != nil {
		t.Fatalf("disabled file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), "a.jar")); !os.IsNotExist(err) {
		t.Fatal("active file should be gone")
	}

	overlay, err := LoadOverlay(root)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := overlay.Mods["a.jar"]
	if !ok {
		t.Fatal("overlay entry missing")
	}
	sum := sha256.Sum256(content)
	if entry.Enabled || entry.Sha256 != hex.EncodeToString(sum[:]) || entry.Size != int64(len(content)) {
		t.Fatalf("unexpected overlay entry: %+v", entry)
	}

	list, err = List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enabled || list[0].Sha256 == "" {
		t.Fatalf("unexpected disabled list: %+v", list)
	}

	if err := SetEnabled(root, "a.jar.disabled", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), "a.jar")); err != nil {
		t.Fatalf("re-enabled file missing: %v", err)
	}
	overlay, err = LoadOverlay(root)
	if err != nil {
		t.Fatal(err)
	}
	if !overlay.Mods["a.jar"].Enabled {
		t.Fatal("overlay should record enabled")
	}
}

func TestToggleRejectsInvalidState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(root, "missing.jar", false); err == nil {
		t.Fatal("missing mod should fail")
	}
	if err := SetEnabled(root, "../evil.jar", false); err == nil {
		t.Fatal("path traversal should fail")
	}
	if err := SetEnabled(root, "notes.txt", false); err == nil {
		t.Fatal("non-jar name should fail")
	}

	if err := os.WriteFile(filepath.Join(Dir(root), "dup.jar"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(root), "dup.jar.disabled"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(root, "dup.jar", false); err == nil {
		t.Fatal("duplicate target should fail")
	}
	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Enabled {
		t.Fatalf("duplicate listing should prefer enabled: %+v", list)
	}
}

func TestListMissingDir(t *testing.T) {
	list, err := List(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}
