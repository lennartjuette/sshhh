package tunnel

import (
	"testing"

	"github.com/lennartjuette/sshhh/internal/config"
	"github.com/lennartjuette/sshhh/internal/proc"
	"github.com/lennartjuette/sshhh/internal/sshcmd"
)

func dyn(port string) []sshcmd.Forward { return []sshcmd.Forward{{Kind: sshcmd.Dynamic, Spec: port}} }

func TestMatch(t *testing.T) {
	tunnels := []config.Tunnel{
		{Name: "co", Host: "gw-co", User: "me@ad", Socks: 1080},
		{Name: "kb", Host: "GW-KB", Socks: 1081},
		{Name: "off", Host: "gw-co", Socks: 1082},
	}
	procs := []proc.Proc{
		{PID: 10, Dest: "someone@else"},                       // plain session: ignored
		{PID: 11, Dest: "me@ad@gw-co", Forwards: dyn("1080")}, // co
		{PID: 12, Dest: "gw-kb", Forwards: dyn("1081")},       // kb, host case differs
		{PID: 13, Dest: "gw-co", Forwards: dyn("9999")},       // same host, other port: external
		{PID: 14, Dest: "me@ad@gw-co", Forwards: dyn("1080")}, // duplicate of co: external
	}
	rows := Match(tunnels, procs)

	want := []struct {
		name   string
		pid    int32
		config bool
	}{
		{"co", 11, true}, {"kb", 12, true}, {"off", 0, true}, {"gw-co", 13, false}, {"gw-co", 14, false},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		r := rows[i]
		pid := int32(0)
		if r.Proc != nil {
			pid = r.Proc.PID
		}
		if r.Name != w.name || pid != w.pid || (r.Config != nil) != w.config {
			t.Errorf("row %d = {%s pid %d config %v}; want %+v", i, r.Name, pid, r.Config != nil, w)
		}
	}
}

func TestRowsStatus(t *testing.T) {
	tunnels := []config.Tunnel{
		{Name: "up", Host: "a", Socks: 1},
		{Name: "starting", Host: "b", Socks: 2},
		{Name: "unknown-sockets", Host: "c", Socks: 3},
		{Name: "down", Host: "d", Socks: 4},
	}
	procs := []proc.Proc{
		{PID: 1, Dest: "a", Forwards: dyn("1"), Listening: map[int]bool{1: true}},
		{PID: 2, Dest: "b", Forwards: dyn("2"), Listening: map[int]bool{}},
		{PID: 3, Dest: "c", Forwards: dyn("3")},
	}
	rows := NewManager("ssh", t.TempDir()).Rows(tunnels, procs)
	for _, r := range rows {
		want := map[string]Status{"up": Up, "starting": Starting, "unknown-sockets": Up, "down": Down}[r.Name]
		if r.Status != want {
			t.Errorf("%s: status %v; want %v", r.Name, r.Status, want)
		}
	}
	if rows[0].PID != 1 || rows[3].PID != 0 {
		t.Errorf("PIDs = %d, %d", rows[0].PID, rows[3].PID)
	}
}
