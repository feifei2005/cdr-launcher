//go:build windows

package netproxy

import "testing"

func TestSplitProxyServer(t *testing.T) {
	tests := []struct {
		server string
		http   string
		https  string
	}{
		{"127.0.0.1:7890", "http://127.0.0.1:7890", "http://127.0.0.1:7890"},
		{"http=127.0.0.1:7897;https=127.0.0.1:7897", "http://127.0.0.1:7897", "http://127.0.0.1:7897"},
		{"https=127.0.0.1:7897", "", "http://127.0.0.1:7897"},
		{"socks=127.0.0.1:7891", "socks5://127.0.0.1:7891", "socks5://127.0.0.1:7891"},
		{"http=host:1;https=host:2", "http://host:1", "http://host:2"},
		{"", "", ""},
	}
	for _, tt := range tests {
		httpProxy, httpsProxy := splitProxyServer(tt.server)
		if httpProxy != tt.http || httpsProxy != tt.https {
			t.Errorf("splitProxyServer(%q) = (%q, %q)，期望 (%q, %q)", tt.server, httpProxy, httpsProxy, tt.http, tt.https)
		}
	}
}

func TestNormalizeBypass(t *testing.T) {
	noProxy, localOnly := normalizeBypass("<local>;*.example.com; 10.0.0.0/8")
	if !localOnly {
		t.Fatal("<local> 应标记为仅本机绕过")
	}
	want := "*.example.com,10.0.0.0/8,localhost,127.0.0.1,::1"
	if noProxy != want {
		t.Fatalf("normalizeBypass = %q，期望 %q", noProxy, want)
	}
	if _, again := normalizeBypass(""); again {
		t.Fatal("空例外列表不应包含 <local>")
	}
}

func TestIsLocalHostname(t *testing.T) {
	local := []string{"localhost", "mypc", "127.0.0.1", "::1", ""}
	for _, host := range local {
		if !isLocalHostname(host) {
			t.Errorf("%q 应视为本机", host)
		}
	}
	for _, host := range []string{"littleskin.cn", "api.minecraftservices.com", "8.8.8.8"} {
		if isLocalHostname(host) {
			t.Errorf("%q 不应视为本机", host)
		}
	}
}
