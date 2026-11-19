package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennartjuette/sshhh/internal/sshcmd"
)

func TestExampleParses(t *testing.T) {
	tunnels, err := Parse(strings.NewReader(Example))
	if err != nil {
		t.Fatal(err)
	}
	if len(tunnels) != 1 || !tunnels[0].Autostart {
		t.Fatalf("got %+v", tunnels)
	}
	spec := tunnels[0].Spec()
	want := []sshcmd.Forward{{Kind: sshcmd.Dynamic, Spec: "1080"}, {Kind: sshcmd.Local, Spec: "8080:intranet:80"}}
	if !sshcmd.SameForwards(spec.Forwards, want) || spec.Destination() != "me@gateway.example.com" || spec.Port != 22 {
		t.Errorf("Spec = %+v", spec)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"unknown key":     "[[tunnel]]\nname='a'\nhost='h'\nsocks=1\nautostrat=true",
		"name is missing": "[[tunnel]]\nhost='h'\nsocks=1",
		"used twice":      "[[tunnel]]\nname='a'\nhost='h'\nsocks=1\n[[tunnel]]\nname='a'\nhost='h'\nsocks=2",
		"host is missing": "[[tunnel]]\nname='a'\nsocks=1",
		"needs socks":     "[[tunnel]]\nname='a'\nhost='h'",
		"toml":            "[[tunnel]\nname=",
	}
	for want, input := range tests {
		_, err := Parse(strings.NewReader(input))
		if err == nil || (want != "toml" && !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

func TestEmptyConfig(t *testing.T) {
	tunnels, err := Parse(strings.NewReader("# nothing yet\n"))
	if err != nil || len(tunnels) != 0 {
		t.Errorf("got %v, %v", tunnels, err)
	}
}

func TestPaths(t *testing.T) {
	t.Setenv("SSHHH_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.FromSlash("/cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.FromSlash("/state"))
	if got, want := DefaultPath(), filepath.FromSlash("/cfg/sshhh/tunnels.toml"); got != want {
		t.Errorf("DefaultPath = %q; want %q", got, want)
	}
	if got, want := StateDir(), filepath.FromSlash("/state/sshhh"); got != want {
		t.Errorf("StateDir = %q; want %q", got, want)
	}
	t.Setenv("SSHHH_CONFIG", "x.toml")
	if DefaultPath() != "x.toml" {
		t.Error("SSHHH_CONFIG ignored")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnels.toml")
	if _, err := Load(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: err = %v; want fs.ErrNotExist", err)
	}
	if err := os.WriteFile(path, []byte(Example), 0o600); err != nil {
		t.Fatal(err)
	}
	if tunnels, err := Load(path); err != nil || len(tunnels) != 1 {
		t.Errorf("Load = %v, %v", tunnels, err)
	}
	if err := os.WriteFile(path, []byte("[[tunnel]]\nname='a'"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("errors must name the file: %v", err)
	}
}

func TestEnsure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "dir", "tunnels.toml")
	created, err := Ensure(path)
	if err != nil || !created {
		t.Fatalf("Ensure = %v, %v", created, err)
	}
	tunnels, err := Load(path)
	if err != nil || len(tunnels) != 0 {
		t.Fatalf("the starter config must load as empty: %v, %v", tunnels, err)
	}
	if err := os.WriteFile(path, []byte(Example), 0o600); err != nil {
		t.Fatal(err)
	}
	if created, err := Ensure(path); err != nil || created {
		t.Errorf("Ensure must keep an existing file: %v, %v", created, err)
	}
	if tunnels, _ := Load(path); len(tunnels) != 1 {
		t.Error("existing file was overwritten")
	}
}

func TestStarterUncommentsToExample(t *testing.T) {
	setting := regexp.MustCompile(`^# (\[\[|\w+\s*=)`) // what a person would uncomment
	var lines []string
	for _, l := range strings.Split(Starter, "\n") {
		if setting.MatchString(l) {
			l = strings.TrimPrefix(l, "# ")
		}
		lines = append(lines, l)
	}
	tunnels, err := Parse(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil || len(tunnels) != 1 {
		t.Errorf("removing the # must give a working tunnel: %v, %v", tunnels, err)
	}
}

func TestSyntaxErrorNamesLine(t *testing.T) {
	_, err := Parse(strings.NewReader("[[tunnel]]\nname = 'a'\nhost = \n"))
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("err = %v", err)
	}
}

func TestReconnectDefaultsToOn(t *testing.T) {
	tunnels, err := Parse(strings.NewReader("[[tunnel]]\nname='a'\nhost='h'\nsocks=1\n[[tunnel]]\nname='b'\nhost='h'\nsocks=2\nreconnect=false"))
	if err != nil {
		t.Fatal(err)
	}
	if !tunnels[0].Reconnects() || tunnels[1].Reconnects() {
		t.Errorf("Reconnects = %v, %v; want true, false", tunnels[0].Reconnects(), tunnels[1].Reconnects())
	}
}
