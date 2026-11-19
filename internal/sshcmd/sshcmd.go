// Package sshcmd builds and parses OpenSSH command lines that forward ports.
package sshcmd

import (
	"slices"
	"strconv"
	"strings"
)

// Kind is the type of a port forward, named after its ssh flag.
type Kind byte

const (
	Dynamic Kind = 'D' // -D: SOCKS proxy
	Local   Kind = 'L' // -L: local port to remote target
	Remote  Kind = 'R' // -R: server port to local target
)

// Forward is one forwarding spec as passed to ssh, e.g. {Local, "8080:db:5432"}.
type Forward struct {
	Kind Kind
	Spec string
}

func (f Forward) String() string { return string(f.Kind) + ":" + f.Spec }

// ListenPort returns the port ssh listens on locally for this forward.
// ok is false for remote forwards and for forwards bound to a Unix socket.
func (f Forward) ListenPort() (port int, ok bool) {
	fields := splitFields(f.Spec)
	var s string
	switch {
	case f.Kind == Dynamic:
		s = fields[len(fields)-1] // [bind:]port
	case f.Kind == Local && len(fields) == 3:
		s = fields[0] // port:host:hostport
	case f.Kind == Local && len(fields) == 4:
		s = fields[1] // bind:port:host:hostport
	default:
		return 0, false
	}
	port, err := strconv.Atoi(s)
	return port, err == nil
}

// splitFields splits a forwarding spec at colons outside of [IPv6] brackets.
func splitFields(spec string) []string {
	var fields []string
	depth, start := 0, 0
	for i, r := range spec {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ':':
			if depth == 0 {
				fields = append(fields, spec[start:i])
				start = i + 1
			}
		}
	}
	return append(fields, spec[start:])
}

// SameForwards reports whether a and b hold the same forwards in any order.
func SameForwards(a, b []Forward) bool {
	return len(a) == len(b) && slices.Equal(sorted(a), sorted(b))
}

func sorted(fwds []Forward) []Forward {
	s := slices.Clone(fwds)
	slices.SortFunc(s, func(x, y Forward) int { return strings.Compare(x.String(), y.String()) })
	return s
}

// Spec describes an ssh connection that only forwards ports.
type Spec struct {
	User        string
	Host        string
	Port        int
	Compression bool
	Forwards    []Forward
	Options     []string // extra ssh arguments, placed before the destination
}

// Destination returns "user@host", or just the host if no user is set.
func (s Spec) Destination() string {
	if s.User == "" {
		return s.Host
	}
	return s.User + "@" + s.Host
}

// Args returns the ssh arguments, without the program name. The connection
// runs no remote command, exits if a forward cannot be set up and notices a
// dead server within about 45 seconds.
func (s Spec) Args() []string {
	args := []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
	}
	if s.Compression {
		args = append(args, "-C")
	}
	if s.Port != 0 {
		args = append(args, "-p", strconv.Itoa(s.Port))
	}
	for _, f := range s.Forwards {
		args = append(args, "-"+string(f.Kind), f.Spec)
	}
	args = append(args, s.Options...)
	return append(args, s.Destination())
}

// flagsWithValue lists the ssh options that take an argument, see ssh(1).
const flagsWithValue = "BbcDEeFIiJLlmOoPpQRSWw"

// Parse extracts the forwards and the destination from an ssh command line.
// argv[0] is the program name. dest is empty if argv names no destination.
func Parse(argv []string) (fwds []Forward, dest string) {
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			if i+1 < len(argv) {
				dest = argv[i+1]
			}
			return fwds, dest
		}
		if len(arg) < 2 || arg[0] != '-' {
			return fwds, arg
		}
		// Flags can be grouped ("-NCD1080"). The first one that takes a value
		// consumes the rest of the word, or the next word if nothing is left.
		j := strings.IndexAny(arg[1:], flagsWithValue)
		if j < 0 {
			continue
		}
		kind, val := Kind(arg[1+j]), arg[2+j:]
		if val == "" && i+1 < len(argv) {
			i++
			val = argv[i]
		}
		if kind == Dynamic || kind == Local || kind == Remote {
			fwds = append(fwds, Forward{kind, val})
		}
	}
	return fwds, ""
}

// Host returns the lower-cased host of an ssh destination such as
// "user@host", "user@domain@host" or "ssh://user@host:2222".
func Host(dest string) string {
	dest = strings.TrimPrefix(dest, "ssh://")
	if i := strings.LastIndexByte(dest, '@'); i >= 0 {
		dest = dest[i+1:]
	}
	if strings.Count(dest, ":") == 1 {
		dest, _, _ = strings.Cut(dest, ":")
	}
	return strings.ToLower(dest)
}
