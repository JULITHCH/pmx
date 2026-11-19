---
name: pmx
description: Inspect and operate Proxmox LXC containers with the pmx CLI – list servers and containers (status, IPs, tags), run commands inside containers via pct exec, read logs/configs, and start/stop/reboot containers. Use whenever the user mentions Proxmox, PVE, LXC containers, CTs, a container ID like "101", or one of their Proxmox servers by name and wants to look at, debug, change or manage something on it.
---

# pmx – Proxmox LXC access

`pmx` reads the user's servers from `~/.config/pmx/config.toml` and talks to the
Proxmox hosts over SSH (reused ControlMaster connection, so repeated calls are fast).

## Rules

- **Only use the non-interactive commands below.** `pmx` with no arguments,
  `pmx <server>`, `pmx <server> <ct>` and `pmx -s …` open a TUI or an interactive
  shell. They need a TTY and will hang or fail in the Bash tool.
- **Ask the user before** `shutdown`, `stop`, `reboot`, or any `exec` that
  changes state (installing packages, editing configs, restarting services,
  deleting files). Read-only commands need no confirmation.
- Prefer numeric container IDs once known. A name lookup costs an extra listing call.
- Append `< /dev/null` to `pmx exec` when not piping input, so a command that
  reads stdin can't block.

## Commands

```sh
pmx servers --json                    # configured servers: name, host, user, port
pmx list --json                       # all containers on all servers (queried in parallel)
pmx list <server> [<server>…] --json  # only some servers
pmx exec <server> <ct> '<command>'    # run via `pct exec <id> -- sh -c '<command>'` as root
pmx start|shutdown|stop|reboot <server> <ct>
```

- `<server>`: name from the config, case-insensitive, unique prefix is enough.
- `<ct>`: numeric ID, or the container's hostname / unique part of it.
- `list --json` fields: `server, node, id, name, status (running|stopped|…), ips[], tags[], cores, memory_mb`.
  For stopped containers, `ips` shows the static IPs from the config (if any).
- `exec`: everything after `<ct>` is the command, run by `sh` inside the container.
  Pass it as one single-quoted string. stdout/stderr and the exit code come
  straight from the command. Exit code **255** means SSH/pct failed (check stderr).
  stdin is forwarded, which is handy for writing files:
  `printf '%s\n' "$content" | pmx exec pve1 101 'cat > /etc/app/config.yml'`
- `shutdown` is graceful, `stop` is a hard stop. Prefer `shutdown`.

## Typical workflows

```sh
# Find a container
pmx list --json | jq -r '.[] | "\(.server)\t\(.id)\t\(.name)\t\(.status)\t\(.ips|join(","))"'

# Diagnose
pmx exec pve1 101 'uptime; df -h /; free -m' < /dev/null
pmx exec pve1 101 'systemctl --failed --no-pager' < /dev/null
pmx exec pve1 101 'journalctl -u nginx -n 100 --no-pager' < /dev/null
pmx exec pve1 101 'cat /etc/os-release' < /dev/null
```

Containers can be Alpine or other minimal images: there may be no `bash`,
`systemctl` or `journalctl`. Check `/etc/os-release` first if a command is missing.

## Errors

| stderr contains | Meaning / what to do |
|---|---|
| `Permission denied (publickey…)` | The user's SSH key isn't on that Proxmox host. Ask the user to run `! pmx` once and pick the server: pmx offers `ssh-copy-id`. Don't try to work around it. |
| `Connection timed out` / `No route to host` | Host unreachable (VPN? offline?). Tell the user. Don't retry in a loop. |
| `a password is required` | Server uses `sudo = true` without NOPASSWD sudo. Config/sudoers issue for the user. |
| `no server "…" in the config` | Run `pmx servers` and use a listed name. |
| `… is ambiguous: …` | Use the numeric ID or a more specific name. |
| `CT … not running` (from pct) | Container is stopped. Ask before `pmx start`. |
| `/etc/pve not found` | The configured host isn't a Proxmox node. |

If `pmx` itself is missing: `go install github.com/JULITHCH/pmx@latest`
(source: https://github.com/JULITHCH/pmx).
