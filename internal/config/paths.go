package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const appName = "sshhh"

// DefaultPath returns $SSHHH_CONFIG, or tunnels.toml in the user's config
// directory: $XDG_CONFIG_HOME or ~/.config on Unix (macOS included, as CLI
// users expect), %AppData% on Windows.
func DefaultPath() string {
	if p := os.Getenv("SSHHH_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(baseDir("XDG_CONFIG_HOME", ".config", os.UserConfigDir), appName, "tunnels.toml")
}

// StateDir returns the directory for tunnel logs: $XDG_STATE_HOME or
// ~/.local/state on Unix, %LocalAppData% on Windows.
func StateDir() string {
	return filepath.Join(baseDir("XDG_STATE_HOME", filepath.Join(".local", "state"), os.UserCacheDir), appName)
}

func baseDir(env, homeRel string, windows func() (string, error)) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if d, err := windows(); err == nil {
			return d
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, homeRel)
}
