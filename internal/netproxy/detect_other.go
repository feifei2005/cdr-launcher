//go:build !windows

package netproxy

func detect() Config {
	if cfg, ok := envConfig(); ok {
		return cfg
	}
	return Config{Mode: "direct"}
}
