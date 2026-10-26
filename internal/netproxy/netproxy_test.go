package netproxy

import (
	"net/url"
	"reflect"
	"testing"
)

func TestJavaProxyArgs(t *testing.T) {
	httpProxy, _ := url.Parse("http://127.0.0.1:7890")
	httpsProxy, _ := url.Parse("http://127.0.0.1:7891")
	got := javaProxyArgs(httpProxy, httpsProxy, "*.example.com, 10.0.0.0/8")
	want := []string{
		"-Dhttp.proxyHost=127.0.0.1", "-Dhttp.proxyPort=7890",
		"-Dhttps.proxyHost=127.0.0.1", "-Dhttps.proxyPort=7891",
		"-Dhttp.nonProxyHosts=*.example.com|10.0.0.0/8",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("javaProxyArgs = %v，期望 %v", got, want)
	}
}

func TestJavaProxyArgsSocks(t *testing.T) {
	socks, _ := url.Parse("socks5://127.0.0.1:1080")
	got := javaProxyArgs(socks, socks, "")
	want := []string{
		"-DsocksProxyHost=127.0.0.1", "-DsocksProxyPort=1080",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("javaProxyArgs = %v，期望 %v", got, want)
	}
}

func TestJavaProxyArgsAuthAndDefaultPort(t *testing.T) {
	proxy, _ := url.Parse("http://user:pass@proxy.local")
	got := javaProxyArgs(proxy, nil, "")
	want := []string{
		"-Dhttp.proxyHost=proxy.local", "-Dhttp.proxyPort=80",
		"-Dhttp.proxyUser=user", "-Dhttp.proxyPassword=pass",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("javaProxyArgs = %v，期望 %v", got, want)
	}
}

func TestJavaNonProxyHosts(t *testing.T) {
	if got := javaNonProxyHosts("<local>,*.a.com"); got != "*.a.com" {
		t.Fatalf("javaNonProxyHosts = %q", got)
	}
	if got := javaNonProxyHosts(""); got != "" {
		t.Fatalf("javaNonProxyHosts = %q", got)
	}
}
