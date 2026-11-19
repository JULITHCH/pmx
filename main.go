package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "0.1.0"

const usageText = `pmx – Schnellzugriff auf Proxmox LXC Container

Aufruf:
  pmx                       interaktive Auswahl (Server → Container)
  pmx <server>              direkt zur Container-Liste eines Servers
  pmx <server> <ct>         direkt per pct enter in den Container (ID oder Name)
  pmx -s <server> <ct>      direkt per SSH in den Container
  pmx list [server...]      Container aller/bestimmter Server als Tabelle
  pmx init                  Beispiel-Config anlegen
  pmx edit                  Config im $EDITOR öffnen
  pmx version

Optionen:
  -c <datei>   Config-Datei (Standard: %s, oder $PMX_CONFIG)
  -s, --ssh    SSH statt pct enter (nur mit <server> <ct>)

Tasten Serverliste:    enter Container · ctrl+s SSH auf Host · r Status · e Config · / Filter · q Ende
Tasten Containerliste: enter pct enter · ctrl+s SSH in Container · H Host-Shell
                       s Start · x Shutdown · R Reboot · c IP kopieren · a nur laufende
                       r neu laden · / Filter · esc zurück · ? Hilfe · q Ende
`

func main() {
	cfgPath := defaultConfigPath()
	useSSH := false
	var args []string
	argv := os.Args[1:]
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; a {
		case "-c", "--config":
			if i+1 >= len(argv) {
				die(errors.New("-c benötigt einen Pfad"))
			}
			i++
			cfgPath = argv[i]
		case "-s", "--ssh":
			useSSH = true
		case "-h", "--help", "help":
			fmt.Printf(usageText, tildify(defaultConfigPath()))
			return
		case "-v", "--version", "version":
			fmt.Println("pmx", version)
			return
		default:
			if strings.HasPrefix(a, "-") {
				die(fmt.Errorf("unbekannte Option %s (pmx --help)", a))
			}
			args = append(args, a)
		}
	}

	if len(args) > 0 {
		switch args[0] {
		case "init":
			if err := writeExampleConfig(cfgPath); err != nil {
				die(err)
			}
			fmt.Println("Config angelegt:", cfgPath)
			return
		case "edit":
			ed := first(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
			os.Exit(runInteractive(exec.Command("sh", "-c", ed+` "$1"`, "sh", cfgPath)))
		}
	}

	cfg, err := LoadConfig(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeExampleConfig(cfgPath); err != nil {
			die(err)
		}
		fmt.Printf("Noch keine Config vorhanden – Beispiel angelegt unter:\n  %s\n\nServer eintragen (pmx edit) und pmx erneut starten.\n", cfgPath)
		return
	}
	if err != nil {
		die(err)
	}
	if len(cfg.Servers) == 0 {
		die(fmt.Errorf("keine Server in %s – mit `pmx edit` eintragen", cfgPath))
	}

	if len(args) > 0 && (args[0] == "list" || args[0] == "ls") {
		os.Exit(listCmd(cfg, args[1:]))
	}

	var start *Server
	if len(args) >= 1 {
		if start, err = cfg.Find(args[0]); err != nil {
			die(err)
		}
	}
	if len(args) >= 2 {
		os.Exit(direct(start, args[1], useSSH))
	}

	p := tea.NewProgram(newModel(cfgPath, cfg, start), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "pmx:", err)
	os.Exit(1)
}

func runInteractive(cmd *exec.Cmd) int {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "pmx:", err)
		return 1
	}
	return 0
}

func askYN(q string) bool {
	fmt.Printf("%s [y/N] ", q)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "y", "j", "yes", "ja":
		return true
	}
	return false
}

// ensureKey sorgt (nach Rückfrage) für einen lokalen Public Key.
func ensureKey(s *Server) (string, bool) {
	pub, ok := s.PubKeyPath()
	if ok {
		return pub, true
	}
	if !askYN(fmt.Sprintf("Kein lokaler SSH-Key gefunden – %s erzeugen?", tildify(pub))) {
		return "", false
	}
	return pub, runInteractive(KeygenCmd(pub)) == 0
}

// direct verbindet ohne TUI direkt mit einem Container.
func direct(s *Server, ref string, useSSH bool) int {
	// Schnellster Weg: numerische ID + pct enter braucht keine Liste.
	if id, err := strconv.Atoi(ref); err == nil && !useSSH {
		return runInteractive(s.EnterCmd(id))
	}

	_, cts, err := s.ListContainers()
	if isAuthErr(err) {
		pub, ok := ensureKey(s)
		if ok && askYN(fmt.Sprintf("Kein SSH-Key auf %s hinterlegt – mit ssh-copy-id hinterlegen?", s.Target())) &&
			runInteractive(s.CopyIDCmd(pub)) == 0 {
			_, cts, err = s.ListContainers()
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pmx:", err)
		return 1
	}
	c, err := FindContainer(cts, ref)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pmx:", err)
		return 1
	}
	if !c.Running() {
		if !askYN(fmt.Sprintf("CT %d (%s) ist gestoppt – starten?", c.ID, c.Name)) {
			return 1
		}
		if err := s.Pct("start", c.ID); err != nil {
			fmt.Fprintln(os.Stderr, "pmx:", err)
			return 1
		}
		if useSSH { // IP neu ermitteln
			if _, cts, err = s.ListContainers(); err == nil {
				c, _ = FindContainer(cts, strconv.Itoa(c.ID))
			}
		}
	}
	if !useSSH {
		return runInteractive(s.EnterCmd(c.ID))
	}

	ip := c.PrimaryIP()
	if ip == "" {
		fmt.Fprintf(os.Stderr, "pmx: keine IP für CT %d bekannt\n", c.ID)
		return 1
	}
	err = s.CheckContainerSSH(ip)
	if isAuthErr(err) {
		pub, ok := ensureKey(s)
		if !ok || !askYN(fmt.Sprintf("Kein SSH-Key für %s@%s hinterlegt – per pct hinterlegen?", s.ContainerUser, ip)) {
			return 1
		}
		data, rerr := os.ReadFile(pub)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "pmx:", rerr)
			return 1
		}
		if err := s.PushContainerKey(c.ID, s.ContainerUser, strings.TrimSpace(string(data))); err != nil {
			fmt.Fprintln(os.Stderr, "pmx:", err)
			return 1
		}
		err = nil
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pmx:", err)
		return 1
	}
	return runInteractive(s.ContainerShellCmd(ip))
}

func listCmd(cfg *Config, names []string) int {
	var servers []*Server
	if len(names) == 0 {
		for i := range cfg.Servers {
			servers = append(servers, &cfg.Servers[i])
		}
	}
	for _, n := range names {
		s, err := cfg.Find(n)
		if err != nil {
			die(err)
		}
		servers = append(servers, s)
	}

	type result struct {
		node string
		cts  []Container
		err  error
	}
	res := make([]result, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, c, err := s.ListContainers()
			res[i] = result{n, c, err}
		}()
	}
	wg.Wait()

	rc := 0
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVER\tID\tSTATUS\tNAME\tIP\tTAGS")
	for i, s := range servers {
		if res[i].err != nil {
			fmt.Fprintf(os.Stderr, "pmx: %s: %v\n", s.Name, res[i].err)
			rc = 1
			continue
		}
		for _, c := range res[i].cts {
			fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n", s.Name, c.ID, strings.ToLower(c.Status), c.Name,
				strings.Join(c.IPs, ","), strings.Join(c.Tags, ","))
		}
	}
	tw.Flush()
	return rc
}
