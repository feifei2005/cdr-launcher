// Package netproxy 让启动器的所有出站请求跟随 Windows 系统代理。
package netproxy

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// Config 是一次代理探测的结果。
type Config struct {
	proxy   func(*url.URL) (*url.URL, error)
	Mode    string
	Address string
	NoProxy string
}

// Detect 解析当前代理：环境变量优先，其次 Windows 系统代理。
func Detect() Config {
	return detect()
}

// Apply 让 Transport 每次请求都按当前系统代理设置决定走向。
func Apply(transport *http.Transport) {
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if cfg := Detect(); cfg.proxy != nil {
			return cfg.proxy(req.URL)
		}
		return nil, nil
	}
}

// NewTransport 返回带系统代理设置的默认 Transport。
func NewTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	Apply(transport)
	return transport
}

// NewClient 返回带系统代理设置的 HTTP 客户端。
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: NewTransport()}
}

// JavaArgs 返回让游戏 JVM 走同一代理的 -D 参数。
func JavaArgs() []string {
	cfg := Detect()
	args := []string{"-Djava.net.useSystemProxies=true"}
	if cfg.proxy == nil {
		return args
	}
	probe := func(scheme string) *url.URL {
		u, err := cfg.proxy(&url.URL{Scheme: scheme, Host: "example.com"})
		if err != nil {
			return nil
		}
		return u
	}
	return append(args, javaProxyArgs(probe("http"), probe("https"), cfg.NoProxy)...)
}

func javaProxyArgs(httpProxy, httpsProxy *url.URL, noProxy string) []string {
	var args []string
	if isSocks(httpProxy) || isSocks(httpsProxy) {
		socks := httpProxy
		if !isSocks(socks) {
			socks = httpsProxy
		}
		host, port := proxyHostPort(socks, "1080")
		args = append(args, "-DsocksProxyHost="+host, "-DsocksProxyPort="+port)
		if socks.User != nil {
			args = append(args, "-DsocksProxyUser="+socks.User.Username())
			if pass, ok := socks.User.Password(); ok {
				args = append(args, "-DsocksProxyPassword="+pass)
			}
		}
	} else {
		if httpProxy != nil {
			host, port := proxyHostPort(httpProxy, "80")
			args = append(args, "-Dhttp.proxyHost="+host, "-Dhttp.proxyPort="+port)
			args = append(args, httpProxyAuth("http", httpProxy)...)
		}
		if httpsProxy != nil {
			host, port := proxyHostPort(httpsProxy, "80")
			args = append(args, "-Dhttps.proxyHost="+host, "-Dhttps.proxyPort="+port)
			args = append(args, httpProxyAuth("https", httpsProxy)...)
		}
	}
	if hosts := javaNonProxyHosts(noProxy); hosts != "" {
		args = append(args, "-Dhttp.nonProxyHosts="+hosts)
	}
	return args
}

func httpProxyAuth(prefix string, u *url.URL) []string {
	if u.User == nil {
		return nil
	}
	args := []string{"-D" + prefix + ".proxyUser=" + u.User.Username()}
	if pass, ok := u.User.Password(); ok {
		args = append(args, "-D"+prefix+".proxyPassword="+pass)
	}
	return args
}

func isSocks(u *url.URL) bool {
	if u == nil {
		return false
	}
	switch u.Scheme {
	case "socks", "socks4", "socks5":
		return true
	}
	return false
}

func proxyHostPort(u *url.URL, defaultPort string) (string, string) {
	port := u.Port()
	if port == "" {
		port = defaultPort
	}
	return u.Hostname(), port
}

// javaNonProxyHosts 把逗号分隔的例外列表转成 Java 的竖线格式。
func javaNonProxyHosts(noProxy string) string {
	var items []string
	for _, part := range strings.Split(noProxy, ",") {
		part = strings.TrimSpace(part)
		if part == "" || strings.EqualFold(part, "<local>") {
			continue
		}
		items = append(items, part)
	}
	return strings.Join(items, "|")
}

// Status 返回可展示的代理说明。
func Status() string {
	cfg := Detect()
	switch cfg.Mode {
	case "system":
		return "系统代理 · " + cfg.Address
	case "env":
		return "环境变量代理 · " + cfg.Address
	case "pac":
		return "检测到 PAC 自动配置脚本，未启用（请改用全局代理）"
	default:
		return "直连"
	}
}

// envConfig 读取 HTTP_PROXY / HTTPS_PROXY / NO_PROXY。
func envConfig() (Config, bool) {
	env := httpproxy.FromEnvironment()
	if env.HTTPProxy == "" && env.HTTPSProxy == "" {
		return Config{}, false
	}
	address := env.HTTPSProxy
	if address == "" {
		address = env.HTTPProxy
	}
	return Config{proxy: env.ProxyFunc(), Mode: "env", Address: displayAddress(address), NoProxy: env.NoProxy}, true
}

func displayAddress(raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return raw
}
