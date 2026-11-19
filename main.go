package main

import (
	"bufio"
	"encoding/json"
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

const usageText = `pmx – fast access to Proxmox LXC containers

Usage:
  pmx                       interactive picker (server → container)
  pmx <server>              jump straight to a server's container list
  pmx <server> <ct>         pct enter into a container right away (ID or name)
  pmx -s <server> <ct>      SSH into a container right away
  pmx list [server...]      table of containers on all/given servers
  pmx servers               list configured servers

Non-interactive (scripts, automation):
  pmx exec <server> <ct> <command>     run a command in a container (pct exec);
                                       stdin is forwarded, exit code is passed through
  pmx start|shutdown|stop|reboot <server> <ct>
  pmx init                  create an example config
  pmx edit                  open the config in $EDITOR
  pmx version

Options:
  -c <file>    config file (default: %s, or $PMX_CONFIG)
  -s, --ssh    use SSH instead of pct enter (only with <server> <ct>)
  --json       JSON output for list / servers

Server list keys:    enter containers · ctrl+s SSH to host · r status · e config · / filter · q quit
Container list keys: enter pct enter · ctrl+s SSH into container · H host shell
                     s start · x shutdown · R reboot · c copy IP · a running only
                     r reload · / filter · esc back · ? help · q quit
`

func main() {
	cfgPath := defaultConfigPath()
	useSSH, asJSON := false, false
	var args []string
	argv := os.Args[1:]
	for i := 0; i < len(argv); i++ {
		// Everything after `exec <server> <ct>` (or after `--`) is the command itself.
		if len(args) == 3 && args[0] == "exec" {
			args = append(args, argv[i:]...)
			break
		}
		switch a := argv[i]; a {
		case "--":
			args = append(args, argv[i+1:]...)
			i = len(argv)
		case "--json":
			asJSON = true
		case "-c", "--config":
			if i+1 >= len(argv) {
				die(errors.New("-c requires a path"))
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
				die(fmt.Errorf("unknown option %s (see pmx --help)", a))
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
			fmt.Println("Config created:", cfgPath)
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
		fmt.Printf("No config found – created an example at:\n  %s\n\nAdd your servers (pmx edit) and run pmx again.\n", cfgPath)
		return
	}
	if err != nil {
		die(err)
	}
	if len(cfg.Servers) == 0 {
		die(fmt.Errorf("no servers in %s – add them with `pmx edit`", cfgPath))
	}

	if len(args) > 0 {
		switch args[0] {
		case "list", "ls":
			os.Exit(listCmd(cfg, args[1:], asJSON))
		case "servers":
			os.Exit(serversCmd(cfg, asJSON))
		case "exec":
			os.Exit(execCmd(cfg, args[1:]))
		case "start", "shutdown", "stop", "reboot":
			os.Exit(powerCmd(cfg, args[0], args[1:]))
		}
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

// ensureKey makes sure a local public key exists (asking first).
func ensureKey(s *Server) (string, bool) {
	pub, ok := s.PubKeyPath()
	if ok {
		return pub, true
	}
	if !askYN(fmt.Sprintf("No local SSH key found – generate %s?", tildify(pub))) {
		return "", false
	}
	return pub, runInteractive(KeygenCmd(pub)) == 0
}

// direct connects to a container without the TUI.
func direct(s *Server, ref string, useSSH bool) int {
	// Fastest path: a numeric ID with pct enter needs no listing.
	if id, err := strconv.Atoi(ref); err == nil && !useSSH {
		return runInteractive(s.EnterCmd(id))
	}

	_, cts, err := s.ListContainers()
	if isAuthErr(err) {
		pub, ok := ensureKey(s)
		if ok && askYN(fmt.Sprintf("Your SSH key is not installed on %s – install it with ssh-copy-id?", s.Target())) &&
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
		if !askYN(fmt.Sprintf("CT %d (%s) is stopped – start it?", c.ID, c.Name)) {
			return 1
		}
		if err := s.Pct("start", c.ID); err != nil {
			fmt.Fprintln(os.Stderr, "pmx:", err)
			return 1
		}
		if useSSH { // fetch the IP again
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
		fmt.Fprintf(os.Stderr, "pmx: no known IP for CT %d\n", c.ID)
		return 1
	}
	err = s.CheckContainerSSH(ip)
	if isAuthErr(err) {
		pub, ok := ensureKey(s)
		if !ok || !askYN(fmt.Sprintf("Your SSH key is not installed for %s@%s – install it via pct?", s.ContainerUser, ip)) {
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

func listCmd(cfg *Config, names []string, asJSON bool) int {
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
	if asJSON {
		type jsonCt struct {
			Server string   `json:"server"`
			Node   string   `json:"node"`
			ID     int      `json:"id"`
			Name   string   `json:"name"`
			Status string   `json:"status"`
			IPs    []string `json:"ips"`
			Tags   []string `json:"tags"`
			Cores  string   `json:"cores,omitempty"`
			Memory string   `json:"memory_mb,omitempty"`
		}
		out := []jsonCt{}
		for i, s := range servers {
			if res[i].err != nil {
				fmt.Fprintf(os.Stderr, "pmx: %s: %v\n", s.Name, res[i].err)
				rc = 1
				continue
			}
			for _, c := range res[i].cts {
				out = append(out, jsonCt{s.Name, res[i].node, c.ID, c.Name, strings.ToLower(c.Status),
					orEmpty(c.IPs), orEmpty(c.Tags), c.Cores, c.Memory})
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
		return rc
	}
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

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func serversCmd(cfg *Config, asJSON bool) int {
	if asJSON {
		type jsonSrv struct {
			Name string `json:"name"`
			Host string `json:"host"`
			User string `json:"user"`
			Port int    `json:"port"`
		}
		out := []jsonSrv{}
		for _, s := range cfg.Servers {
			out = append(out, jsonSrv{s.Name, s.Host, s.User, s.Port})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTARGET\tPORT")
	for _, s := range cfg.Servers {
		fmt.Fprintf(tw, "%s\t%s\t%d\n", s.Name, s.Target(), s.Port)
	}
	tw.Flush()
	return 0
}

// resolveCt finds server and container ID. A numeric ID needs no listing.
func resolveCt(cfg *Config, srvRef, ctRef string) (*Server, int, error) {
	s, err := cfg.Find(srvRef)
	if err != nil {
		return nil, 0, err
	}
	if id, err := strconv.Atoi(ctRef); err == nil {
		return s, id, nil
	}
	_, cts, err := s.ListContainers()
	if err != nil {
		return nil, 0, err
	}
	c, err := FindContainer(cts, ctRef)
	return s, c.ID, err
}

func execCmd(cfg *Config, args []string) int {
	if len(args) < 3 {
		die(errors.New("usage: pmx exec <server> <ct> <command>"))
	}
	s, id, err := resolveCt(cfg, args[0], args[1])
	if err != nil {
		die(err)
	}
	return s.Exec(id, strings.Join(args[2:], " "))
}

func powerCmd(cfg *Config, action string, args []string) int {
	if len(args) != 2 {
		die(fmt.Errorf("usage: pmx %s <server> <ct>", action))
	}
	s, id, err := resolveCt(cfg, args[0], args[1])
	if err != nil {
		die(err)
	}
	if err := s.Pct(action, id); err != nil {
		die(err)
	}
	past := map[string]string{"start": "started", "shutdown": "shut down", "stop": "stopped", "reboot": "rebooted"}
	fmt.Printf("CT %d %s\n", id, past[action])
	return 0
}
