package launcher

import "testing"

func TestAllowedNoRules(t *testing.T) {
	if !Allowed(nil, HostOS(), nil) {
		t.Fatal("no rules should be allowed")
	}
}

func TestAllowedOSRules(t *testing.T) {
	windows := OS{Name: "windows", Arch: "x86_64"}
	osx := OS{Name: "osx", Arch: "arm64"}

	winOnly := []Rule{{Action: "allow", OS: &RuleOS{Name: "windows"}}}
	if !Allowed(winOnly, windows, nil) {
		t.Error("windows rule should allow windows")
	}
	if Allowed(winOnly, osx, nil) {
		t.Error("windows rule should reject osx")
	}

	notWin := []Rule{
		{Action: "allow"},
		{Action: "disallow", OS: &RuleOS{Name: "windows"}},
	}
	if Allowed(notWin, windows, nil) {
		t.Error("disallow should override earlier allow")
	}
	if !Allowed(notWin, osx, nil) {
		t.Error("non-windows should stay allowed")
	}

	x86Only := []Rule{{Action: "allow", OS: &RuleOS{Name: "universal", Arch: "x86"}}}
	if Allowed(x86Only, OS{Name: "universal", Arch: "x86_64"}, nil) {
		t.Error("x86 rule must not match x86_64")
	}
}

func TestAllowedFeatures(t *testing.T) {
	rules := []Rule{{Action: "allow", Features: map[string]bool{"is_demo_user": true}}}
	if Allowed(rules, HostOS(), nil) {
		t.Error("unset feature should not match")
	}
	if !Allowed(rules, HostOS(), map[string]bool{"is_demo_user": true}) {
		t.Error("enabled feature should match")
	}
}
