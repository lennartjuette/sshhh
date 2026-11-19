// Command sshhh keeps an eye on your SSH tunnels.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"
	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/tunnel"
	"github.com/lennartjuette/sshhh/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = ""

func main() {
	configPath := flag.String("config", config.DefaultPath(), "tunnel configuration `file`")
	sshBin := flag.String("ssh", "ssh", "ssh `binary` to run")
	showVersion := flag.Bool("version", false, "print the version and exit")
	printExample := flag.Bool("example", false, "print an example configuration and exit")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println("sshhh", buildVersion())
		return
	case *printExample:
		fmt.Print(config.Example)
		return
	}

	// First run: create a commented config, so the UI can open it with "e".
	if _, err := config.Ensure(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, "sshhh: cannot create config:", err)
		os.Exit(1)
	}

	mgr := tunnel.NewManager(*sshBin, config.StateDir())
	if _, err := tea.NewProgram(ui.New(mgr, *configPath, buildVersion())).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sshhh:", err)
		os.Exit(1)
	}
}

// buildVersion prefers the version set at build time, then the module
// version that "go install …@v1.2.3" records.
func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
