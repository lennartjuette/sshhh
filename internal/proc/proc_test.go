package proc

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/net"
)

func TestCountSockets(t *testing.T) {
	conn := func(local uint32, status string) net.ConnectionStat {
		return net.ConnectionStat{Laddr: net.Addr{IP: "127.0.0.1", Port: local}, Status: status}
	}
	listening, clients := countSockets([]net.ConnectionStat{
		conn(1080, "LISTEN"),
		conn(1080, "LISTEN"), // IPv4 and IPv6 socket on the same port
		conn(8080, "LISTEN"),
		conn(1080, "ESTABLISHED"),
		conn(8080, "ESTABLISHED"),
		conn(8080, "TIME_WAIT"),
		conn(53122, "ESTABLISHED"), // the connection to the ssh server
	})
	if want := map[int]bool{1080: true, 8080: true}; !maps.Equal(listening, want) {
		t.Errorf("listening = %v; want %v", listening, want)
	}
	if clients != 2 {
		t.Errorf("clients = %d; want 2", clients)
	}
}

func TestIsSSH(t *testing.T) {
	for name, want := range map[string]bool{"ssh": true, "SSH.EXE": true, "sshd": false, "ssh-agent": false} {
		if isSSH(name) != want {
			t.Errorf("isSSH(%q) != %v", name, want)
		}
	}
}

// TestSnapshot only checks that reading the process table works on this OS.
func TestSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
}
