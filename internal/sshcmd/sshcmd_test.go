package sshcmd

import (
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		cmd  string
		fwds []Forward
		dest string
	}{
		{"ssh host", nil, "host"},
		{"ssh -N -v -p 22 -C -D 1080 me@corp.example@gw", []Forward{{Dynamic, "1080"}}, "me@corp.example@gw"},
		{"ssh -NCD1080 -o X=Y -L8080:db:5432 gw ls -l", []Forward{{Dynamic, "1080"}, {Local, "8080:db:5432"}}, "gw"},
		{"ssh -R 9000:localhost:3000 -i key gw", []Forward{{Remote, "9000:localhost:3000"}}, "gw"},
		{"ssh -p2222 -- gw", nil, "gw"},
		{"ssh -L 1:h:2", []Forward{{Local, "1:h:2"}}, ""},
		{"ssh -L", []Forward{{Local, ""}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			fwds, dest := Parse(strings.Fields(tt.cmd))
			if !slices.Equal(fwds, tt.fwds) || dest != tt.dest {
				t.Errorf("Parse = %v, %q; want %v, %q", fwds, dest, tt.fwds, tt.dest)
			}
		})
	}
}

func TestArgsRoundTrip(t *testing.T) {
	spec := Spec{
		User: "me", Host: "gw", Port: 2222, Compression: true,
		Forwards: []Forward{{Dynamic, "1080"}, {Local, "8080:db:5432"}, {Remote, "9:x:1"}},
		Options:  []string{"-o", "IdentitiesOnly=yes"},
	}
	args := spec.Args()
	if args[0] != "-N" || args[len(args)-1] != "me@gw" {
		t.Fatalf("Args = %v", args)
	}
	fwds, dest := Parse(append([]string{"ssh"}, args...))
	if !SameForwards(fwds, spec.Forwards) || dest != "me@gw" {
		t.Errorf("Parse(Args) = %v, %q", fwds, dest)
	}
}

func TestListenPort(t *testing.T) {
	tests := []struct {
		f    Forward
		port int
		ok   bool
	}{
		{Forward{Dynamic, "1080"}, 1080, true},
		{Forward{Dynamic, "localhost:1080"}, 1080, true},
		{Forward{Dynamic, "[::1]:1080"}, 1080, true},
		{Forward{Local, "8080:db:5432"}, 8080, true},
		{Forward{Local, "127.0.0.1:8080:db:5432"}, 8080, true},
		{Forward{Local, "8080:[fe80::1]:5432"}, 8080, true},
		{Forward{Local, "/tmp/sock:db:5432"}, 0, false},
		{Forward{Remote, "9000:localhost:3000"}, 0, false},
	}
	for _, tt := range tests {
		port, ok := tt.f.ListenPort()
		if port != tt.port || ok != tt.ok {
			t.Errorf("%v.ListenPort() = %d, %v; want %d, %v", tt.f, port, ok, tt.port, tt.ok)
		}
	}
}

func TestSameForwards(t *testing.T) {
	a := []Forward{{Dynamic, "1"}, {Local, "2:h:3"}}
	b := []Forward{{Local, "2:h:3"}, {Dynamic, "1"}}
	if !SameForwards(a, b) {
		t.Error("order must not matter")
	}
	if SameForwards(a, a[:1]) || SameForwards(a, []Forward{{Dynamic, "1"}, {Local, "2:h:4"}}) {
		t.Error("different forwards reported as equal")
	}
}

func TestHost(t *testing.T) {
	for in, want := range map[string]string{
		"gw":                 "gw",
		"me@GW.example":      "gw.example",
		"me@corp.example@gw": "gw",
		"ssh://me@gw:2222":   "gw",
		"fe80::1":            "fe80::1",
		"":                   "",
	} {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q; want %q", in, got, want)
		}
	}
}
