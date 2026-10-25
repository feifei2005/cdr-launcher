package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	exeDir := filepath.Join(dir, "launcher")
	if err := os.MkdirAll(filepath.Join(exeDir, ".minecraft"), 0o755); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path, exeDir)
	if err != nil {
		t.Fatalf("Load defaults: %v", err)
	}
	if loaded.InstanceDir != filepath.Join(exeDir, ".minecraft") {
		t.Errorf("InstanceDir = %q", loaded.InstanceDir)
	}
	if loaded.MemoryMB != DefaultMemoryMB {
		t.Errorf("MemoryMB = %d, want %d", loaded.MemoryMB, DefaultMemoryMB)
	}

	loaded.PlayerName = "Steve_01"
	loaded.MemoryMB = 8192
	if err := Save(path, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load(path, exeDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.PlayerName != "Steve_01" || reloaded.MemoryMB != 8192 {
		t.Errorf("round trip mismatch: %+v", reloaded)
	}
}

func TestNormalize(t *testing.T) {
	s := Settings{MemoryMB: 100, InstanceDir: "  "}
	s.Normalize(`C:\launcher`)
	if s.MemoryMB != DefaultMemoryMB {
		t.Errorf("MemoryMB = %d, want default", s.MemoryMB)
	}
	if s.PlayerName != "Player" || s.Channel != DefaultChannel {
		t.Errorf("defaults not applied: %+v", s)
	}
	if !filepath.IsAbs(s.InstanceDir) {
		t.Errorf("InstanceDir = %q, want absolute", s.InstanceDir)
	}
}

func TestValidate(t *testing.T) {
	s := Defaults(`C:\launcher`)
	if err := s.Validate(); err != nil {
		t.Errorf("defaults should validate: %v", err)
	}
	s.PlayerName = "中文名"
	if err := s.Validate(); err == nil {
		t.Error("expected invalid player name error")
	}
	if ValidPlayerName("Alex_01") != true || ValidPlayerName("ab") != false {
		t.Error("ValidPlayerName mismatch")
	}
}
