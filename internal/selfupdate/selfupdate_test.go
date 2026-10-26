package selfupdate

import (
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.10.0", "1.9.9", 1},
		{"2", "2.0.0", 0},
		{"1.2", "1.2.1", -1},
		{"1.0.0-beta", "1.0.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestResolveDownloadURL(t *testing.T) {
	got, err := resolveDownloadURL("cdr-launcher-1.0.1.exe")
	if err != nil {
		t.Fatalf("相对地址应可解析: %v", err)
	}
	if !strings.HasPrefix(got, "https://mc-api.xn--suki-uf1gk54ba.cn/pack/launcher/") {
		t.Errorf("相对地址解析结果异常: %s", got)
	}

	if _, err := resolveDownloadURL("https://example.com/launcher.exe"); err != nil {
		t.Errorf("HTTPS 绝对地址应通过: %v", err)
	}
	if _, err := resolveDownloadURL("http://example.com/launcher.exe"); err == nil {
		t.Error("HTTP 地址应被拒绝")
	}
	if _, err := resolveDownloadURL("ftp://example.com/launcher.exe"); err == nil {
		t.Error("非 HTTPS 协议应被拒绝")
	}
}

func TestValidSHA(t *testing.T) {
	if !validSHA(strings.Repeat("a", 64)) {
		t.Error("64 位十六进制应通过")
	}
	if validSHA(strings.Repeat("a", 63)) {
		t.Error("长度不足应被拒绝")
	}
	if validSHA(strings.Repeat("z", 64)) {
		t.Error("非十六进制字符应被拒绝")
	}
}
