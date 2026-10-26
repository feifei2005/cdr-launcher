//go:build windows

package netproxy

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func detect() Config {
	if cfg, ok := envConfig(); ok {
		return cfg
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return Config{Mode: "direct"}
	}
	defer key.Close()

	enabled, _, _ := key.GetIntegerValue("ProxyEnable")
	server, _, _ := key.GetStringValue("ProxyServer")
	override, _, _ := key.GetStringValue("ProxyOverride")
	pac, _, _ := key.GetStringValue("AutoConfigURL")

	if enabled == 0 || strings.TrimSpace(server) == "" {
		if strings.TrimSpace(pac) != "" {
			return Config{Mode: "pac"}
		}
		return Config{Mode: "direct"}
	}

	httpProxy, httpsProxy := splitProxyServer(server)
	if httpProxy == "" && httpsProxy == "" {
		return Config{Mode: "direct"}
	}

	noProxy, localOnly := normalizeBypass(override)
	proxyCfg := httpproxy.Config{HTTPProxy: httpProxy, HTTPSProxy: httpsProxy, NoProxy: noProxy}
	base := proxyCfg.ProxyFunc()
	proxy := func(u *url.URL) (*url.URL, error) {
		if localOnly && isLocalHostname(u.Hostname()) {
			return nil, nil
		}
		return base(u)
	}
	return Config{proxy: proxy, Mode: "system", Address: displayAddress(httpsProxy), NoProxy: noProxy}
}

// splitProxyServer 解析 Windows 的 ProxyServer，支持 host:port 与分协议写法。
func splitProxyServer(server string) (httpProxy, httpsProxy string) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", ""
	}
	if !strings.Contains(server, "=") {
		u := proxyURL("http", server)
		return u, u
	}
	values := map[string]string{}
	for _, part := range strings.Split(server, ";") {
		scheme, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		value = strings.TrimSpace(value)
		if scheme == "" || value == "" {
			continue
		}
		values[scheme] = value
	}
	// SOCKS 在 Windows 上覆盖所有协议；未列出的协议按直连处理。
	socksProxy := schemeProxy(values, "socks5")
	if socksProxy == "" {
		socksProxy = schemeProxy(values, "socks")
	}
	httpProxy = schemeProxy(values, "http")
	if httpProxy == "" {
		httpProxy = socksProxy
	}
	httpsProxy = schemeProxy(values, "https")
	if httpsProxy == "" {
		httpsProxy = socksProxy
	}
	return httpProxy, httpsProxy
}

func schemeProxy(values map[string]string, scheme string) string {
	raw, ok := values[scheme]
	if !ok {
		return ""
	}
	return proxyURL(scheme, raw)
}

func proxyURL(scheme, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	switch scheme {
	case "socks", "socks4", "socks5":
		return "socks5://" + raw
	default:
		return "http://" + raw
	}
}

// normalizeBypass 把 Windows 的 ProxyOverride 转成 httpproxy 的逗号分隔格式。
func normalizeBypass(override string) (string, bool) {
	var items []string
	localOnly := false
	for _, part := range strings.Split(override, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.EqualFold(part, "<local>") {
			localOnly = true
			continue
		}
		items = append(items, part)
	}
	items = append(items, "localhost", "127.0.0.1", "::1")
	return strings.Join(items, ","), localOnly
}

// isLocalHostname 对应 ProxyOverride 中 <local> 的语义：不带点的主机名。
func isLocalHostname(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return !strings.Contains(host, ".")
}
