# wsl-ssh-pageant

[![Release](https://github.com/deese/wsl-ssh-pageant/actions/workflows/release.yml/badge.svg)](https://github.com/deese/wsl-ssh-pageant/actions/workflows/release.yml)

Bridges a Pageant-compatible SSH agent (PuTTY, gpg4win) to WSL and the native Windows OpenSSH client via a Unix socket or a named pipe.

## Download

Grab the latest release on the [releases page](https://github.com/deese/wsl-ssh-pageant/releases).

Two binaries are provided:

| Binary | Description |
|--------|-------------|
| `wsl-ssh-pageant-amd64.exe` | Console binary. Opens a terminal window when double-clicked. |
| `wsl-ssh-pageant-amd64-gui.exe` | No console window. Suitable for autostart and systray use. |

## Usage

### WSL

1. Start Pageant or a compatible agent (e.g. gpg4win).
2. Run:
   ```
   wsl-ssh-pageant-amd64.exe --wsl C:\wsl-ssh-pageant\ssh-agent.sock
   ```
3. In WSL, set the environment variable:
   ```bash
   export SSH_AUTH_SOCK=/mnt/c/wsl-ssh-pageant/ssh-agent.sock
   ```
4. SSH keys from Pageant are now available inside WSL.

> The socket path must be under a path accessible from WSL and no longer than ~100 characters.
>
> **Security:** place the socket in a directory whose ACL restricts access to your user (e.g. under your user profile). Paths like `C:\wsl-ssh-pageant\` are world-readable by default, which means any local user or WSL instance can connect to the socket and use your SSH keys.

### Windows native OpenSSH

1. Start Pageant or a compatible agent.
2. Run:
   ```
   wsl-ssh-pageant-amd64.exe --winssh ssh-pageant
   ```
3. Set the environment variable (or add it to your user environment variables):
   ```
   set SSH_AUTH_SOCK=\\.\pipe\ssh-pageant
   ```
4. SSH keys from Pageant are now available in `cmd.exe` and PowerShell.

### Systray

To show an icon in the system tray while the agent is running, use the `--systray` flag. Use the gui binary to avoid a console window:

```
wsl-ssh-pageant-amd64-gui.exe --systray --winssh ssh-pageant
```

The tray icon provides a **Quit** menu entry to stop the agent cleanly.

### Running both at once

`--wsl` and `--winssh` can be combined in a single process:

```
wsl-ssh-pageant-amd64.exe --wsl C:\wsl-ssh-pageant\ssh-agent.sock --winssh ssh-pageant
```

## Running at login

The gui zip includes `install.ps1`, which creates a shortcut in your Windows Startup folder so the agent starts automatically at login.

**Install (PowerShell, run once from the extracted folder):**

```powershell
.\install.ps1
```

This creates a shortcut with `--systray --winssh ssh-pageant`. Parameters:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `-PipeName` | Named pipe name | `ssh-pageant` |
| `-WslSocket` | Unix socket path for WSL1 | _(none)_ |
| `-NoSystray` | Disable tray icon | _(systray enabled)_ |
| `-Uninstall` | Remove the shortcut | — |

**Examples:**

```powershell
# Custom pipe name, no tray icon
.\install.ps1 -PipeName my-agent -NoSystray

# WSL1 socket + named pipe
.\install.ps1 -WslSocket "C:\Users\you\ssh-agent.sock"

# Remove
.\install.ps1 -Uninstall
```

## WSL2 support

WSL2 runs inside a Hyper-V VM and does not support AF_UNIX socket communication with the Windows host. The `--wsl` flag cannot work in WSL2 due to this OS-level limitation. The `--winssh` named pipe works fine and is the basis for the recommended workaround.

### Workaround: socat + npiperelay

On the Windows side, run as usual:

```
wsl-ssh-pageant-amd64-gui.exe --winssh ssh-pageant
```

On the WSL2 side, install [`npiperelay.exe`](https://github.com/jstarks/npiperelay/releases) and `socat`, then add to your `~/.bashrc` or `~/.zshrc`:

```bash
sudo apt install socat

socat UNIX-LISTEN:/tmp/ssh-agent.sock,fork,unlink-early \
  EXEC:"/mnt/c/Users/YOUR_USER/bin/npiperelay.exe -ei -s //./pipe/ssh-pageant" &

export SSH_AUTH_SOCK=/tmp/ssh-agent.sock
```

### With systemd (WSL2 modern builds)

If your WSL2 has systemd enabled (`systemd=true` in `/etc/wsl.conf`), a service unit is more reliable:

```ini
# ~/.config/systemd/user/ssh-agent.service
[Unit]
Description=SSH agent relay to Windows named pipe

[Service]
ExecStart=/usr/bin/socat \
  UNIX-LISTEN:%t/ssh-agent.sock,fork \
  EXEC:'/mnt/c/Users/YOUR_USER/bin/npiperelay.exe -ei -s //./pipe/ssh-pageant',nofork

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now ssh-agent
echo 'export SSH_AUTH_SOCK=$XDG_RUNTIME_DIR/ssh-agent.sock' >> ~/.bashrc
```

## Building from source

Go 1.20 or later is required.

```powershell
cd src
go run github.com/go-bindata/go-bindata/go-bindata -pkg main -o assets.go assets/
go build -o wsl-ssh-pageant-amd64.exe .
go build -ldflags "-H=windowsgui" -o wsl-ssh-pageant-amd64-gui.exe .
```

## Windows version requirements

- **WSL socket support (`--wsl`):** Windows 10 1803 or later (first version with `AF_UNIX` socket support).
- **Named pipe (`--winssh`):** any Windows 10 version with the native OpenSSH client installed.

## FAQ

**Why does the gui binary close immediately?**
The gui binary does not open a console window by design. Run it with `--systray` so it stays alive with a tray icon, or register it via Task Scheduler. Without `--systray` and without a blocking flag it exits immediately.

**Can I use both `--wsl` and `--winssh` at the same time?**
Yes, a single process handles both simultaneously.

## Credits

- [Ben Pye](https://github.com/benpye) for the first implementation of this tool.
- [John Starks](https://github.com/jstarks/) for [npiperelay](https://github.com/jstarks/npiperelay/), an early reference for WSL↔Windows bridging.
- [Mark Dietzer](https://github.com/Doridian) for contributions to the original .NET implementation.
