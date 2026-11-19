# pmx – Schnellzugriff auf Proxmox LXC Container

```
pmx  →  Server wählen  →  Container wählen  →  du bist drin (pct enter)
                                exit  →  zurück zur Container-Auswahl
```

## Installation

```sh
go install github.com/JULITHCH/pmx@latest
# oder aus dem Repo:
make install          # baut und kopiert nach ~/.local/bin/pmx
pmx init              # legt ~/.config/pmx/config.toml an
pmx edit              # Server eintragen
```

## Config (`~/.config/pmx/config.toml`)

```toml
[defaults]
user = "root"            # SSH-User auf dem Proxmox-Host
container_user = "root"  # User für SSH direkt in den Container (ctrl+s)
jump = true              # Container-SSH über den Proxmox-Host tunneln
# identity_file = "~/.ssh/id_ed25519"
# sudo = false

[[server]]
name = "pve1"
host = "192.168.1.10"

[[server]]
name = "pve2"
host = "pve2.lan"
user = "admin"
sudo = true              # braucht NOPASSWD-sudo für pct/lxc-info
port = 2222
```

Alle Werte aus `[defaults]` können pro Server überschrieben werden.
Bei einem Cluster jeden Node als eigenen `[[server]]` eintragen – `pct enter`
funktioniert nur auf dem Node, auf dem der Container liegt.

## Bedienung

**Serverliste**

| Taste    | Aktion                                   |
|----------|------------------------------------------|
| `enter`  | Container des Servers anzeigen           |
| `ctrl+s` | SSH-Session auf dem Proxmox-Host         |
| `r`      | Erreichbarkeit neu prüfen                |
| `e`      | Config im `$EDITOR` öffnen (lädt danach neu) |
| `/`      | Filtern                                  |
| `q`      | Beenden                                  |

**Containerliste** (ID, Name, IP, vCPU, RAM, Tags; gestoppte grau)

| Taste       | Aktion                                                 |
|-------------|--------------------------------------------------------|
| `enter`     | `pct enter` – gestoppte Container werden auf Wunsch gestartet |
| `ctrl+s`    | SSH direkt in den Container (über die IP)              |
| `H`         | Shell auf dem Proxmox-Host                             |
| `s` / `x` / `R` | Start / Shutdown / Reboot (mit Rückfrage)          |
| `c`         | IP in die Zwischenablage kopieren                      |
| `a`         | nur laufende Container zeigen                          |
| `r`         | neu laden                                              |
| `/`         | Filtern (ID, Name, IP, Tags)                           |
| `esc`       | zurück zur Serverliste                                 |
| `?`         | alle Tasten anzeigen                                   |
| `q`         | Beenden                                                |

Nach `exit` aus `pct enter` oder SSH landest du wieder in der Container-Auswahl.

## SSH-Keys

- **Proxmox-Host:** Akzeptiert der Host deinen Key nicht, fragt pmx, ob er per
  `ssh-copy-id` hinterlegt werden soll (einmalig das Passwort eingeben).
- **Container (`ctrl+s`):** Fehlt der Key im Container, fragt pmx, ob er
  hinterlegt werden soll. Das geht über `pct exec` auf dem Host und braucht
  **kein Container-Passwort**. Im Container muss ein sshd laufen.
- Gibt es lokal noch gar keinen Key, bietet pmx an, einen neuen ed25519-Key zu erzeugen.

## Ohne TUI

```sh
pmx pve1                 # direkt zur Container-Liste von pve1
pmx pve1 101             # direkt pct enter in CT 101
pmx pve1 web             # Container per Name (oder eindeutigem Teil davon)
pmx -s pve1 web          # per SSH statt pct enter
pmx list                 # Tabelle aller Container aller Server (parallel)
pmx list pve1 | grep running
```

Server-Namen dürfen abgekürzt werden, solange sie eindeutig sind (`pmx pv 101`).

## Warum ist das schnell?

- Die Container-Liste kommt in **einem** SSH-Aufruf. Status und IPs liest pmx per
  `lxc-info` statt per `pct status` / `pct exec`.
- pmx verwendet die SSH-Verbindung zum Host weiter (`ControlMaster`, 10 min).
  Ab dem zweiten Aufruf entfällt der Verbindungsaufbau, das gilt auch für den
  Tunnel bei `ctrl+s`.
- `pmx <server> <id>` geht ohne Umweg direkt in den Container.

## Voraussetzungen

Go ≥ 1.24 zum Bauen, lokal `ssh` (OpenSSH) und für Key-Setup `ssh-copy-id` / `ssh-keygen`.
Auf den Proxmox-Hosts genügen die Standardwerkzeuge (`pct`, `lxc-info`).

## Hinweise

- Unbekannte Host-Keys werden beim ersten Kontakt automatisch akzeptiert
  (`StrictHostKeyChecking=accept-new`), geänderte Host-Keys weiterhin abgelehnt.
- Bei gestoppten Containern werden statische IPs aus der Container-Config angezeigt.
- Mit `sudo = true` muss der User `pct`, `lxc-info` und `sh` ohne Passwort per sudo
  ausführen dürfen (z. B. `admin ALL=(root) NOPASSWD: ALL`).
