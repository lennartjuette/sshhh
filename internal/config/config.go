// Package config loads the tunnel definitions from a TOML file.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lennartjuette/sshhh/internal/sshcmd"
	"github.com/pelletier/go-toml/v2"
)

// Example is a commented sample configuration.
const Example = `# sshhh tunnels: one [[tunnel]] block per connection.

[[tunnel]]
name        = "office"                       # shown in the list, must be unique
host        = "gateway.example.com"          # host name or alias from ~/.ssh/config
user        = "me"                           # optional
port        = 22                             # optional
socks       = 1080                           # -D: SOCKS proxy on localhost:1080
local       = ["8080:intranet:80"]           # -L: localhost:8080 -> intranet:80
remote      = []                             # -R: server port -> local target
options     = ["-o", "IdentitiesOnly=yes"]   # extra ssh arguments
compression = true
autostart   = true                           # start when sshhh starts
reconnect   = true                           # restart after a drop (default)
`

// Tunnel is one [[tunnel]] block of the configuration.
type Tunnel struct {
	Name        string   `toml:"name"`
	Host        string   `toml:"host"`
	User        string   `toml:"user"`
	Port        int      `toml:"port"`
	Socks       int      `toml:"socks"`
	Local       []string `toml:"local"`
	Remote      []string `toml:"remote"`
	Options     []string `toml:"options"`
	Compression bool     `toml:"compression"`
	Autostart   bool     `toml:"autostart"`
	Reconnect   *bool    `toml:"reconnect"` // nil means true
}

// Reconnects reports whether sshhh restarts the tunnel after it drops.
func (t Tunnel) Reconnects() bool { return t.Reconnect == nil || *t.Reconnect }

// Spec returns the ssh connection the tunnel describes.
func (t Tunnel) Spec() sshcmd.Spec {
	var fwds []sshcmd.Forward
	if t.Socks != 0 {
		fwds = append(fwds, sshcmd.Forward{Kind: sshcmd.Dynamic, Spec: strconv.Itoa(t.Socks)})
	}
	for _, s := range t.Local {
		fwds = append(fwds, sshcmd.Forward{Kind: sshcmd.Local, Spec: s})
	}
	for _, s := range t.Remote {
		fwds = append(fwds, sshcmd.Forward{Kind: sshcmd.Remote, Spec: s})
	}
	return sshcmd.Spec{
		User: t.User, Host: t.Host, Port: t.Port, Compression: t.Compression,
		Forwards: fwds, Options: t.Options,
	}
}

// Starter is what Ensure writes on the first run: the example, commented
// out, so that sshhh starts with an empty list and a file ready to fill in.
var Starter = "# Remove the # in front of the [[tunnel]] block below and fill in your own server.\n" +
	"# Press e in sshhh to edit this file, add more blocks for more tunnels.\n\n" +
	commentOut(Example)

func commentOut(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" && !strings.HasPrefix(l, "#") {
			lines[i] = "# " + l
		}
	}
	return strings.Join(lines, "\n")
}

// Ensure writes Starter to path unless the file exists. It reports whether
// it created the file.
func Ensure(path string) (created bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(Starter); err != nil {
		f.Close()
		return true, err
	}
	return true, f.Close()
}

// Load reads and validates the configuration file at path.
func Load(path string) ([]Tunnel, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tunnels, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return tunnels, nil
}

// Parse reads and validates a configuration. Unknown keys are an error, so
// that a typo like "autostrat" does not go unnoticed.
func Parse(r io.Reader) ([]Tunnel, error) {
	var file struct {
		Tunnels []Tunnel `toml:"tunnel"`
	}
	err := toml.NewDecoder(r).DisallowUnknownFields().Decode(&file)
	if strict := (*toml.StrictMissingError)(nil); errors.As(err, &strict) {
		return nil, fmt.Errorf("unknown key:\n%s", strict.String())
	}
	if decode := (*toml.DecodeError)(nil); errors.As(err, &decode) {
		row, _ := decode.Position()
		return nil, fmt.Errorf("line %d: %s", row, decode.Error())
	}
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for i, t := range file.Tunnels {
		switch {
		case t.Name == "":
			return nil, fmt.Errorf("tunnel #%d: name is missing", i+1)
		case seen[t.Name]:
			return nil, fmt.Errorf("tunnel %q: name is used twice", t.Name)
		case t.Host == "":
			return nil, fmt.Errorf("tunnel %q: host is missing", t.Name)
		case len(t.Spec().Forwards) == 0:
			return nil, fmt.Errorf("tunnel %q: needs socks, local or remote", t.Name)
		}
		seen[t.Name] = true
	}
	return file.Tunnels, nil
}
