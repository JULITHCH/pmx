package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sshError trägt Exit-Code und stderr eines fehlgeschlagenen ssh-Aufrufs.
type sshError struct {
	code   int
	stderr string
	err    error
}

func (e *sshError) Error() string {
	msg := lastLine(e.stderr)
	if msg == "" {
		msg = e.err.Error()
	}
	switch {
	case strings.Contains(msg, "Connection refused"):
		return msg + " (läuft sshd?)"
	case strings.Contains(msg, "a password is required"):
		return msg + " (sudo ohne Passwort nötig, siehe README)"
	}
	return msg
}

func isAuthErr(err error) bool {
	var se *sshError
	if !errors.As(err, &se) || se.code != 255 {
		return false
	}
	for _, s := range []string{"Permission denied", "Too many authentication failures", "No more authentication methods"} {
		if strings.Contains(se.stderr, s) {
			return true
		}
	}
	return false
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "Warning: Permanently added") {
			continue
		}
		return l
	}
	return ""
}

// shq quotet einen String für POSIX-Shells.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shq(a)
	}
	return strings.Join(q, " ")
}

func controlDir() string {
	base, err := os.UserCacheDir()
	if err != nil || len(base) > 60 { // Unix-Socket-Pfade sind auf ~104 Zeichen begrenzt
		base = os.TempDir()
	}
	d := filepath.Join(base, "pmx")
	_ = os.MkdirAll(d, 0o700)
	return d
}

func (s *Server) Target() string { return s.User + "@" + s.Host }

func (s *Server) jump() bool { return s.Jump != nil && *s.Jump }

// baseArgs liefert die ssh-Optionen für den Proxmox-Host. Über ControlMaster
// wird eine Verbindung wiederverwendet – dadurch sind Folgeaufrufe sehr schnell.
// escape=true verdoppelt '%' für die Verwendung innerhalb von ProxyCommand.
func (s *Server) baseArgs(escape bool) []string {
	cp := filepath.Join(controlDir(), "%C")
	if escape {
		cp = strings.ReplaceAll(cp, "%", "%%")
	}
	a := []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + cp,
		"-o", "ControlPersist=10m",
		"-o", "ConnectTimeout=5",
		"-o", "ServerAliveInterval=30",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if s.Port != 22 {
		a = append(a, "-p", strconv.Itoa(s.Port))
	}
	if s.IdentityFile != "" {
		a = append(a, "-i", s.IdentityFile)
	}
	return a
}

func (s *Server) sudo(interactive bool) string {
	if !s.Sudo {
		return ""
	}
	if interactive {
		return "sudo "
	}
	return "sudo -n "
}

// runCapture führt cmd aus und sammelt stdout/stderr. WaitDelay verhindert,
// dass ein im Hintergrund weiterlaufender ControlMaster die Pipes blockiert.
func runCapture(cmd *exec.Cmd) (string, error) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = 300 * time.Millisecond
	err := cmd.Run()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return out.String(), &sshError{code: code, stderr: errb.String(), err: err}
	}
	return out.String(), nil
}

// Run führt ein Shell-Skript nicht-interaktiv auf dem Proxmox-Host aus.
func (s *Server) Run(script string) (string, error) {
	args := append(s.baseArgs(false), "-o", "BatchMode=yes", s.Target(), s.sudo(false)+"sh -s")
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	return runCapture(cmd)
}

// EnterCmd: interaktives `pct enter` auf dem Host.
func (s *Server) EnterCmd(id int) *exec.Cmd {
	args := append(s.baseArgs(false), "-t", s.Target(), fmt.Sprintf("%spct enter %d", s.sudo(true), id))
	return exec.Command("ssh", args...)
}

// HostShellCmd: normale SSH-Session auf dem Proxmox-Host.
func (s *Server) HostShellCmd() *exec.Cmd {
	return exec.Command("ssh", append(s.baseArgs(false), s.Target())...)
}

func (s *Server) containerSSHArgs(ip string) []string {
	a := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=5"}
	if s.IdentityFile != "" {
		a = append(a, "-i", s.IdentityFile)
	}
	if s.jump() {
		pc := append([]string{"ssh"}, s.baseArgs(true)...)
		pc = append(pc, "-W", "%h:%p", s.Target())
		a = append(a, "-o", "ProxyCommand="+shellJoin(pc))
	}
	return append(a, s.ContainerUser+"@"+ip)
}

// ContainerShellCmd: SSH direkt in den Container (optional über den Host getunnelt).
func (s *Server) ContainerShellCmd(ip string) *exec.Cmd {
	return exec.Command("ssh", s.containerSSHArgs(ip)...)
}

// CheckContainerSSH prüft, ob ein Login per Key ohne Passwort möglich ist.
func (s *Server) CheckContainerSSH(ip string) error {
	args := append([]string{"-o", "BatchMode=yes"}, s.containerSSHArgs(ip)...)
	_, err := runCapture(exec.Command("ssh", append(args, "true")...))
	return err
}

func (s *Server) CheckHost() error {
	_, err := s.Run("true")
	return err
}

// PubKeyPath liefert den lokalen Public Key und ob er existiert.
func (s *Server) PubKeyPath() (string, bool) {
	if s.IdentityFile != "" {
		p := s.IdentityFile + ".pub"
		return p, fileExists(p)
	}
	for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		p := filepath.Join(homeDir(), ".ssh", n+".pub")
		if fileExists(p) {
			return p, true
		}
	}
	return filepath.Join(homeDir(), ".ssh", "id_ed25519.pub"), false
}

func KeygenCmd(pub string) *exec.Cmd {
	return exec.Command("ssh-keygen", "-t", "ed25519", "-f", strings.TrimSuffix(pub, ".pub"))
}

func (s *Server) CopyIDCmd(pub string) *exec.Cmd {
	return exec.Command("ssh-copy-id", "-i", pub, "-o", "StrictHostKeyChecking=accept-new",
		"-p", strconv.Itoa(s.Port), s.Target())
}

// PushContainerKey hinterlegt den Public Key per `pct exec` im Container –
// ganz ohne Container-Passwort.
func (s *Server) PushContainerKey(id int, user, pubkey string) error {
	inner := `u="$1"; k="$2"
h=$(awk -F: -v u="$u" '$1==u{print $6}' /etc/passwd)
[ -n "$h" ] || { echo "Benutzer $u existiert im Container nicht" >&2; exit 3; }
mkdir -p "$h/.ssh" && chmod 700 "$h/.ssh" && touch "$h/.ssh/authorized_keys" && chmod 600 "$h/.ssh/authorized_keys" || exit 4
grep -qxF "$k" "$h/.ssh/authorized_keys" || printf '%s\n' "$k" >> "$h/.ssh/authorized_keys"
chown -R "$u" "$h/.ssh"
command -v sshd >/dev/null 2>&1 || [ -x /usr/sbin/sshd ] || echo "Hinweis: kein sshd im Container installiert" >&2`
	script := fmt.Sprintf("pct exec %d -- sh -c %s sh %s %s\n", id, shq(inner), shq(user), shq(pubkey))
	_, err := s.Run(script)
	return err
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
