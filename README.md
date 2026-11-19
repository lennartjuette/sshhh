# sshhh

**Shhh… the tunnels are sleeping in the background.**

`sshhh` is a small terminal UI that keeps an eye on your SSH tunnels. Put
your tunnels in a TOML file and sshhh starts them, shows whether they are up
and how many clients use them, and restarts or stops them with one key. It
also finds tunnels that were started some other way.

If you are managing about as many SSH tunnels as you are managing children
and spend your days shushing both, this tool is for you.

![sshhh starting tunnels, connecting one and restarting another](docs/demo.webp)

## Install

**macOS with Homebrew** (the easiest way):

```sh
brew tap lennartjuette/tap
brew trust lennartjuette/tap
brew install --cask sshhh
```

Homebrew asks you to trust a tap before it installs anything from it.
Updates come with `brew upgrade`.

**Download:** grab the file for your system from the
[releases page](https://github.com/lennartjuette/sshhh/releases), unpack it
and run `sshhh` in a terminal.

- macOS: `sshhh_…_darwin_arm64.tar.gz` for Apple silicon, `darwin_amd64` for
  Intel Macs. The binary is not signed by Apple, so macOS blocks it the first
  time. Allow it once with `xattr -d com.apple.quarantine sshhh`, or use
  Homebrew, which does that for you.
- Windows: `sshhh_…_windows_amd64.zip`. Unpack it, open *Terminal* in that
  folder and run `.\sshhh.exe`. Windows 10 and 11 come with the OpenSSH
  client sshhh needs.
- Linux: `sshhh_…_linux_amd64.tar.gz` (or `arm64`). Copy `sshhh` to
  `~/.local/bin` or `/usr/local/bin`.

**With Go:**

```sh
go install github.com/lennartjuette/sshhh@latest
```

sshhh runs `ssh` from your `PATH`, so it works with everything in your
`~/.ssh/config`: aliases, keys, jump hosts and agents.

## Configure

On the first start sshhh creates its config file with a commented example.
Press `e` to open it, remove the `#` in front of the example, fill in your
server and save. sshhh reloads the file when the editor closes. If something
is wrong with it, sshhh says which line and keeps running.

The file lives at `~/.config/sshhh/tunnels.toml`
(`%AppData%\sshhh\tunnels.toml` on Windows). Use `-config` or
`$SSHHH_CONFIG` to point sshhh elsewhere. `e` opens `$VISUAL` or `$EDITOR`,
otherwise nano (or vi) on macOS and Linux and Notepad on Windows.

```toml
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
```

Each tunnel runs as

```sh
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    [-C] [-p port] [-D …] [-L …] [-R …] [options…] [user@]host
```

so a tunnel whose port is taken fails right away, and a tunnel whose server
went away ends within about 45 seconds instead of hanging.

Typos in the config are errors, so `autostrat = true` does not silently
do nothing.

## Use

| Key            | Action                                                        |
| -------------- | ------------------------------------------------------------- |
| `↑` `↓` `k` `j`| select a tunnel                                               |
| `⏎` `space` `s`| start or stop it, or stop a reconnect                          |
| `r`            | restart it                                                    |
| `l`            | show its log in `$PAGER` (default `less`)                     |
| `e`            | edit the config, reloaded when the editor closes              |
| `K`            | terminate all your ssh processes, after asking                |
| `q` `esc`      | quit; asks whether running tunnels should keep running        |
| `ctrl+c`       | quit at once and leave every tunnel running                   |

**Tunnels outlive sshhh.** They run in their own session, so you can quit
sshhh, close the terminal and come back later. On start, sshhh matches the
running ssh processes against the config by host and forwards and adopts
them. Tunnels it cannot match are listed under "not in config" and can be
stopped, but sshhh never stops them on its own.

**Status** is `up` when ssh listens on every local port of the tunnel,
`starting` while it does not yet, `↻ in 4s` while it waits to reconnect,
`failed` when a tunnel with `reconnect = false` exited with an error (the
reason is shown, and the log has the rest), and `down` otherwise. `CONN` counts the client connections currently using the tunnel.

**Logs** of the tunnels sshhh starts go to `~/.local/state/sshhh/<name>.log`
(`%LocalAppData%\sshhh` on Windows).

**Reconnect.** While sshhh runs, it keeps the tunnels of the config up. When
one drops without you stopping it, sshhh starts it again after 1 second, then
waits 2, 4, 8 … seconds after each failed attempt, at most a minute. Once a
tunnel has stayed up for 30 seconds, the next drop starts at 1 second again.
Press `⏎` on a reconnecting tunnel to give up on it, or set
`reconnect = false` to turn it off for that tunnel. Tunnels you stop, kill
with `K` or that are not in the config are never restarted. After you quit
sshhh, tunnels keep running, but nobody watches them until you start sshhh
again.

## Develop

```sh
go test -race ./...
go run . -config ./my-tunnels.toml
```

The tests never touch your real tunnels: the test binary plays a fake `ssh`.

```
main.go               flags, config loading, program start
internal/sshcmd       build and parse ssh command lines
internal/config       TOML config and default paths
internal/proc         find ssh processes and their sockets (gopsutil)
internal/tunnel       match config to processes, start, stop, restart
internal/ui           Bubble Tea model, table, logo animation
```

## Release

Push a tag and GitHub Actions builds the binaries for macOS, Linux and
Windows with [GoReleaser](https://goreleaser.com) and publishes them on the
releases page:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

For the Homebrew cask, create the repository `lennartjuette/homebrew-tap`
once, then add a fine-grained token with write access to its contents as the
secret `HOMEBREW_TAP_TOKEN` in this repository. Without the secret, releases
still work, they just skip Homebrew.

## License

MIT
