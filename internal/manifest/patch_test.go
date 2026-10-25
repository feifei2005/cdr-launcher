package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeApplyPatchRoundTrip(t *testing.T) {
	const size = 2 << 20
	rng := rand.New(rand.NewSource(42))
	oldContent := make([]byte, size)
	rng.Read(oldContent)

	// 新内容在大部相同的基础上插入、删除与改写片段，贴近 mod jar 的版本差异。
	next := make([]byte, 0, size)
	next = append(next, oldContent[:size/3]...)
	next = append(next, []byte("cdr-patch-inserted-block")...)
	next = append(next, oldContent[size/3:size/2]...)
	next = append(next, oldContent[size/2+4096:]...)
	for i := 0; i < 2048 && i < len(next); i++ {
		next[i*157%len(next)] ^= 0x5a
	}

	var buf bytes.Buffer
	if err := EncodePatch(oldContent, bytes.NewReader(next), &buf); err != nil {
		t.Fatalf("EncodePatch: %v", err)
	}
	if buf.Len() >= len(next)/2 {
		t.Errorf("补丁体积过大: %d >= %d/2", buf.Len(), len(next))
	}

	got, err := ApplyPatch(bytes.NewReader(buf.Bytes()), oldContent)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	sum := sha256.Sum256(got)
	want := sha256.Sum256(next)
	if hex.EncodeToString(sum[:]) != hex.EncodeToString(want[:]) {
		t.Fatalf("还原内容哈希不符")
	}
}

// 原始字典不带字典 ID 校验，错误字典可能解码“成功”但内容不符；
// 因此调用方必须以 targetSha256 校验结果。这里确认不会静默得到原内容。
func TestApplyPatchWrongDictionary(t *testing.T) {
	oldContent := bytes.Repeat([]byte("create-delight-remake"), 1<<16)
	want := bytes.Repeat([]byte("create-delight-remake-v2"), 1<<16)
	var buf bytes.Buffer
	if err := EncodePatch(oldContent, bytes.NewReader(want), &buf); err != nil {
		t.Fatalf("EncodePatch: %v", err)
	}
	other := bytes.Repeat([]byte("x"), len(oldContent))
	got, err := ApplyPatch(bytes.NewReader(buf.Bytes()), other)
	if err == nil && bytes.Equal(got, want) {
		t.Fatal("错误字典不应还原出正确内容")
	}
}

func TestDiffAndApply(t *testing.T) {
	fromRoot := t.TempDir()
	toRoot := t.TempDir()
	blobDir := t.TempDir()

	oldJar := bytes.Repeat([]byte("old-jar-payload-"), 1<<17)
	if err := os.MkdirAll(filepath.Join(fromRoot, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(toRoot, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fromRoot, "mods", "a.jar"), oldJar, 0o644); err != nil {
		t.Fatal(err)
	}
	newJar := append(oldJar[:len(oldJar)/2], []byte("new-data")...)
	newJar = append(newJar, oldJar[len(oldJar)/2+1024:]...)
	if err := os.WriteFile(filepath.Join(toRoot, "mods", "a.jar"), newJar, 0o644); err != nil {
		t.Fatal(err)
	}
	// 小于最小差分阈值，走整文件。
	if err := os.WriteFile(filepath.Join(fromRoot, "mods", "small.jar"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toRoot, "mods", "small.jar"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 新版本删除的文件。
	if err := os.WriteFile(filepath.Join(fromRoot, "mods", "removed.jar"), []byte("bye"), 0o644); err != nil {
		t.Fatal(err)
	}

	patches, deletes, err := Diff(DiffOptions{
		FromRoot: fromRoot,
		ToRoot:   toRoot,
		From:     "5.2.0",
		To:       "5.3.0",
		BlobsDir: blobDir,
	})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(patches) != 1 || patches[0].Path != "mods/a.jar" {
		t.Fatalf("补丁列表不符: %+v", patches)
	}
	if len(deletes) != 1 || deletes[0] != "mods/removed.jar" {
		t.Fatalf("删除列表不符: %v", deletes)
	}
	p := patches[0]
	if p.Algo != PatchAlgo || p.From != "5.2.0" || p.To != "5.3.0" {
		t.Fatalf("补丁元数据不符: %+v", p)
	}
	if p.SourceSha256 == "" || p.TargetSha256 == "" || p.SourceSize != int64(len(oldJar)) {
		t.Fatalf("补丁溯源字段缺失: %+v", p)
	}
	sum, err := fileSHA256(filepath.Join(blobDir, p.Sha256))
	if err != nil {
		t.Fatalf("补丁 blob 缺失: %v", err)
	}
	if sum != p.Sha256 {
		t.Fatal("补丁 blob 哈希不符")
	}

	raw, err := os.ReadFile(filepath.Join(blobDir, p.Sha256))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ApplyPatch(bytes.NewReader(raw), oldJar)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	target := sha256.Sum256(newJar)
	gotSum := sha256.Sum256(got)
	if hex.EncodeToString(gotSum[:]) != hex.EncodeToString(target[:]) {
		t.Fatal("补丁还原结果不符")
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"saves/**", "saves/world/level.dat", true},
		{"saves/**", "savesx/a", false},
		{"logs/**", "logs/latest.log", true},
		{"options.txt", "options.txt", true},
		{"options.txt", "config/options.txt", false},
		{"*.jar.disabled", "mods/a.jar.disabled", false},
		{"**/*.jar.disabled", "mods/a.jar.disabled", true},
		{"**/*.jar.disabled", "a.jar.disabled", true},
		{"**/*.part", "mods/x.jar.part", true},
		{"natives-*/**", "natives-windows/x.dll", true},
		{"config/**", "config/sub/a.toml", true},
		{"?.txt", "a.txt", true},
		{"?.txt", "ab.txt", false},
		{"mods/x[1].jar", "mods/x[1].jar", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.rel); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.rel, got, c.want)
		}
	}
}

func TestCleanRelPath(t *testing.T) {
	good := map[string]string{
		"mods/a.jar":    "mods/a.jar",
		"./mods/a.jar":  "mods/a.jar",
		"mods//a.jar":   "mods/a.jar",
		"mods\\a.jar":   "mods/a.jar",
		"config/a/../b": "config/b",
	}
	for in, want := range good {
		got, err := CleanRelPath(in)
		if err != nil {
			t.Fatalf("CleanRelPath(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("CleanRelPath(%q) = %q, want %q", in, got, want)
		}
	}
	bad := []string{"", ".", "..", "../x", "mods/../../x", "/etc/passwd", "C:/Windows"}
	for _, in := range bad {
		if _, err := CleanRelPath(in); err == nil {
			t.Errorf("CleanRelPath(%q) 应报错", in)
		}
	}
}

func TestBuildExcludesProtectedAndSymlinks(t *testing.T) {
	root := t.TempDir()
	must := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("mods/a.jar", "a")
	must("config/c.toml", "c")
	must("options.txt", "opts")
	must("saves/w/level.dat", "level")
	must("logs/latest.log", "log")
	must("mods/off.jar.disabled", "off")

	files, err := Build(BuildOptions{Root: root, SkipBlobs: true})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Path] = true
	}
	if !got["mods/a.jar"] || !got["config/c.toml"] {
		t.Fatalf("缺少应打包文件: %v", got)
	}
	for _, p := range []string{"options.txt", "saves/w/level.dat", "logs/latest.log", "mods/off.jar.disabled"} {
		if got[p] {
			t.Errorf("protected 文件不应进入清单: %s", p)
		}
	}

	link := filepath.Join(root, "mods", "link.jar")
	if err := os.Symlink(filepath.Join(root, "mods", "a.jar"), link); err != nil {
		t.Skipf("创建符号链接失败，跳过: %v", err)
	}
	if _, err := Build(BuildOptions{Root: root, SkipBlobs: true}); err == nil {
		t.Fatal("符号链接应导致 Build 报错")
	}
}
