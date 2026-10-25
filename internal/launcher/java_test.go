package launcher

import "testing"

func TestParseJavaVersion(t *testing.T) {
	output := `Property settings:
    java.home = C:\Program Files\Java\jdk-17
    java.specification.version = 17
    java.version = 17.0.15
    java.vm.name = OpenJDK 64-Bit Server VM

openjdk version "17.0.15" 2026-04-21
`
	info, err := parseJavaVersion(output)
	if err != nil {
		t.Fatalf("parseJavaVersion: %v", err)
	}
	if info.Major != 17 {
		t.Errorf("Major = %d, want 17", info.Major)
	}
	if info.Version != "17.0.15" {
		t.Errorf("Version = %q, want 17.0.15", info.Version)
	}
}

func TestParseJavaVersionLegacy(t *testing.T) {
	info, err := parseJavaVersion("    java.version = 1.8.0_392\n")
	if err != nil {
		t.Fatalf("parseJavaVersion: %v", err)
	}
	if info.Major != 8 {
		t.Errorf("Major = %d, want 8", info.Major)
	}
}

func TestParseJavaVersionInvalid(t *testing.T) {
	if _, err := parseJavaVersion("java version \"25\""); err == nil {
		t.Fatal("expected error for unparseable output")
	}
}

func TestParseJavaMajor(t *testing.T) {
	cases := map[string]int{
		"17":            17,
		"17.0.20.1+1":   17,
		"1.8.0_392":     8,
		`"21.0.12"`:     21,
		"25":            25,
		"":              0,
		"not-a-version": 0,
	}
	for in, want := range cases {
		if got := parseJavaMajor(in); got != want {
			t.Errorf("parseJavaMajor(%q) = %d, want %d", in, got, want)
		}
	}
}
