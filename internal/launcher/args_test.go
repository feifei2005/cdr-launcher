package launcher

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArgumentUnmarshal(t *testing.T) {
	raw := `["--username","${auth_player_name}",{"rules":[{"action":"allow","features":{"has_custom_resolution":true}}],"value":["--width","${resolution_width}"]}]`
	var args []Argument
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(args) != 3 {
		t.Fatalf("len = %d, want 3", len(args))
	}
	if len(args[0].Value) != 1 || args[0].Value[0] != "--username" {
		t.Errorf("plain string arg = %+v", args[0])
	}
	if len(args[2].Rules) != 1 || len(args[2].Value) != 2 {
		t.Errorf("conditional arg = %+v", args[2])
	}
}

func TestOfflinePlayer(t *testing.T) {
	p, err := OfflinePlayer("Steve_01")
	if err != nil {
		t.Fatalf("OfflinePlayer: %v", err)
	}
	if p.UUID != "e4270dab-5764-390b-8cc6-0cf94d9aeee9" {
		t.Errorf("UUID = %s", p.UUID)
	}
	if p.AccessToken != strings.Repeat("0", 32) || p.UserType != "legacy" {
		t.Errorf("player = %+v", p)
	}
	if _, err := OfflinePlayer("ab"); err == nil {
		t.Error("short name should fail")
	}
	if _, err := OfflinePlayer("中文玩家"); err == nil {
		t.Error("non-ascii name should fail")
	}
}

func TestExpandArguments(t *testing.T) {
	args := []Argument{
		{Value: []string{"-Djava.library.path=${natives_directory}"}},
		{Rules: []Rule{{Action: "allow", OS: &RuleOS{Name: "osx"}}}, Value: []string{"-XstartOnFirstThread"}},
		{Value: []string{"--width", "${resolution_width}"}},
	}
	vars := map[string]string{"natives_directory": `C:\natives`}
	got, err := expandArguments(args[:2:2], OS{Name: "windows"}, nil, vars)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 1 || got[0] != `-Djava.library.path=C:\natives` {
		t.Errorf("got %v", got)
	}
	if _, err := expandArguments(args[2:], OS{Name: "windows"}, nil, vars); err == nil {
		t.Error("unknown placeholder should fail")
	}
}
