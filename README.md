# pmx – fast access to Proxmox LXC containers

```
pmx  →  pick a server  →  pick a container  →  you're in (pct enter)
                                 exit  →  back to the container list
```

## Installation

```sh
go install github.com/JULITHCH/pmx@latest
# or from a checkout:
make install          # builds and copies to ~/.local/bin/pmx
pmx init              # creates ~/.config/pmx/config.toml
pmx edit              # add your servers
```

## Config (`~/.config/pmx/config.toml`)

```toml
[defaults]
user = "root"            # SSH user on the Proxmox host
container_user = "root"  # user for SSH directly into a container (ctrl+s)
jump = true              # tunnel container SSH through the Proxmox host
# identity_file = "~/.ssh/id_ed25519"
# sudo = false

[[server]]
name = "pve1"
host = "192.168.1.10"

[[server]]
name = "pve2"
host = "pve2.lan"
user = "admin"
sudo = true              # requires passwordless sudo for pct/lxc-info
port = 2222
```

You can override any value from `[defaults]` per server.
In a cluster, add each node as its own `[[server]]`. `pct enter` only works
on the node that hosts the container.

## Usage

**Server list**

| Key      | Action                                       |
|----------|----------------------------------------------|
| `enter`  | Show the server's containers                 |
| `ctrl+s` | SSH session on the Proxmox host              |
| `r`      | Re-check reachability                        |
| `e`      | Open the config in `$EDITOR` (reloaded after)|
| `/`      | Filter                                       |
| `q`      | Quit                                         |

**Container list** (ID, name, IP, vCPU, RAM, tags; stopped containers are dimmed)

| Key             | Action                                                   |
|-----------------|----------------------------------------------------------|
| `enter`         | `pct enter`. If the container is stopped, pmx offers to start it |
| `ctrl+s`        | SSH directly into the container (via its IP)             |
| `H`             | Shell on the Proxmox host                                |
| `s` / `x` / `R` | Start / shutdown / reboot (with confirmation)            |
| `c`             | Copy IP to clipboard                                     |
| `a`             | Show running containers only                             |
| `r`             | Reload                                                   |
| `/`             | Filter (ID, name, IP, tags)                              |
| `esc`           | Back to the server list                                  |
| `?`             | Show all keys                                            |
| `q`             | Quit                                                     |

When you `exit` from `pct enter` or SSH, you're back in the container list.

## SSH keys

- **Proxmox host:** if the host doesn't accept your key, pmx offers to install
  it with `ssh-copy-id`. You enter the password once.
- **Container (`ctrl+s`):** if your key is missing in the container, pmx offers
  to install it. It does this with `pct exec` on the host, so **you don't need
  the container's password**. The container must run an sshd.
- If you have no local key at all, pmx offers to generate a new ed25519 key.

## Without the TUI

```sh
pmx pve1                 # go straight to pve1's container list
pmx pve1 101             # pct enter into CT 101 right away
pmx pve1 web             # pick a container by name (or a unique part of it)
pmx -s pve1 web          # use SSH instead of pct enter
pmx list                 # table of all containers on all servers (queried in parallel)
pmx list pve1 | grep running
```

You can abbreviate server names as long as they stay unique (`pmx pv 101`).

### Scripting / automation

These commands need no TTY and never prompt. They work in scripts, cron jobs and for AI agents:

```sh
pmx servers [--json]                       # configured servers
pmx list [server...] --json                # containers as JSON
pmx exec pve1 web 'df -h /'                # run a command via pct exec
pmx exec pve1 101 systemctl status nginx   # everything after <ct> is the command
echo "hello" | pmx exec pve1 web 'cat > /tmp/x'   # stdin is forwarded
pmx start|shutdown|stop|reboot pve1 web
```

`pmx exec` passes the command's exit code through (255 means an SSH error).

### Claude Code skill

[`skills/pmx/SKILL.md`](skills/pmx/SKILL.md) teaches Claude Code to use pmx. Install it with:

```sh
mkdir -p ~/.claude/skills && cp -r skills/pmx ~/.claude/skills/
```

## Why is it fast?

- pmx fetches the container list in **one** SSH call. It reads status and
  IPs with `lxc-info` instead of `pct status` / `pct exec`.
- pmx reuses the SSH connection to the host (`ControlMaster`, 10 min).
  From the second call on there's no connection setup. This also applies to
  the `ctrl+s` tunnel.
- `pmx <server> <id>` goes straight into the container.

## Requirements

To build: Go ≥ 1.24. Locally: `ssh` (OpenSSH), plus `ssh-copy-id` / `ssh-keygen`
for key setup. On the Proxmox hosts the standard tools (`pct`, `lxc-info`) are enough.

## Notes

- pmx accepts unknown host keys automatically on first contact
  (`StrictHostKeyChecking=accept-new`). It still rejects changed host keys.
- For stopped containers, pmx shows the static IPs from the container config.
- With `sudo = true`, the user must be allowed to run `pct`, `lxc-info` and `sh`
  through sudo without a password (e.g. `admin ALL=(root) NOPASSWD: ALL`).
