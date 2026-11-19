package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ---------- Styles ----------

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#5B4BDB", Dark: "#A99BFF"}
	cGreen  = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	cRed    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}
	cYellow = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	cDim    = lipgloss.AdaptiveColor{Light: "#8C959F", Dark: "#6E7681"}

	stTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(cAccent).Padding(0, 1)
	stSel    = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	stDim    = lipgloss.NewStyle().Foreground(cDim)
	stGreen  = lipgloss.NewStyle().Foreground(cGreen)
	stRed    = lipgloss.NewStyle().Foreground(cRed)
	stYellow = lipgloss.NewStyle().Foreground(cYellow).Bold(true)
)

// ---------- Tasten ----------

var keys = struct {
	Open, Enter, HostSSH, CtSSH, Back, Ping, Refresh, Start, Stop, Reboot, Copy, Host, Running, Edit, Quit key.Binding
}{
	Open:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "Container")),
	Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "pct enter")),
	HostSSH: key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "ssh Host")),
	CtSSH:   key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "ssh")),
	Back:    key.NewBinding(key.WithKeys("esc", "backspace"), key.WithHelp("esc", "zurück")),
	Ping:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "Status")),
	Refresh: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "neu laden")),
	Start:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "starten")),
	Stop:    key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "herunterfahren")),
	Reboot:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "neustarten")),
	Copy:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "IP kopieren")),
	Host:    key.NewBinding(key.WithKeys("H"), key.WithHelp("H", "Host-Shell")),
	Running: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "nur laufende")),
	Edit:    key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "Config")),
	Quit:    key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "beenden")),
}

// ---------- Items & Delegates ----------

const (
	pingUnknown = iota
	pingOnline
	pingOffline
)

type srvItem struct {
	srv   *Server
	state int
	lat   time.Duration
}

func (i srvItem) FilterValue() string { return i.srv.Name + " " + i.srv.Host }

type srvDelegate struct{ nameW, hostW int }

func (d *srvDelegate) Height() int                         { return 1 }
func (d *srvDelegate) Spacing() int                        { return 0 }
func (d *srvDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d *srvDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it := item.(srvItem)
	sel := index == m.Index()
	cursor := "  "
	name := pad(it.srv.Name, d.nameW)
	if sel {
		cursor = stSel.Render("▸ ")
		name = stSel.Render(name)
	}
	host := it.srv.Target()
	if it.srv.Port != 22 {
		host += ":" + strconv.Itoa(it.srv.Port)
	}
	var st string
	switch it.state {
	case pingOnline:
		st = stGreen.Render("● ") + stDim.Render(it.lat.Round(time.Millisecond).String())
	case pingOffline:
		st = stRed.Render("● offline")
	default:
		st = stDim.Render("○ …")
	}
	line := cursor + name + "  " + stDim.Render(pad(host, d.hostW)) + "  " + st
	fmt.Fprint(w, ansi.Truncate(line, m.Width(), "…"))
}

type ctItem struct{ Container }

func (i ctItem) FilterValue() string {
	return fmt.Sprintf("%d %s %s %s", i.ID, i.Name, strings.Join(i.IPs, " "), strings.Join(i.Tags, " "))
}

type ctDelegate struct{ idW, nameW, ipW int }

func (d *ctDelegate) Height() int                         { return 1 }
func (d *ctDelegate) Spacing() int                        { return 0 }
func (d *ctDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d *ctDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	c := item.(ctItem).Container
	sel := index == m.Index()

	dot := stRed.Render("●")
	switch {
	case c.Running():
		dot = stGreen.Render("●")
	case c.Status != "STOPPED":
		dot = stYellow.Render("●")
	}
	id := pad(strconv.Itoa(c.ID), d.idW)
	name := pad(c.Name, d.nameW)
	ips := pad(strings.Join(c.IPs, ", "), d.ipW)
	var extra []string
	if c.Cores != "" {
		extra = append(extra, c.Cores+" vCPU")
	}
	if c.Memory != "" {
		extra = append(extra, c.Memory+" MB")
	}
	if len(c.Tags) > 0 {
		extra = append(extra, "#"+strings.Join(c.Tags, " #"))
	}
	info := strings.Join(extra, "  ")

	cursor := "  "
	var line string
	switch {
	case sel:
		cursor = stSel.Render("▸ ")
		line = stSel.Render(id+"  "+name) + "  " + ips + "  " + stDim.Render(info)
	case !c.Running():
		line = stDim.Render(id + "  " + name + "  " + ips + "  " + info)
	default:
		line = id + "  " + name + "  " + ips + "  " + stDim.Render(info)
	}
	fmt.Fprint(w, ansi.Truncate(cursor+dot+" "+line, m.Width(), "…"))
}

// pad schneidet ab bzw. füllt rechts mit Leerzeichen auf.
func pad(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-sw)
}

// ---------- Messages ----------

type pingMsg struct {
	name  string
	state int
	lat   time.Duration
}
type ctsMsg struct {
	seq  int
	srv  *Server
	node string
	cts  []Container
	err  error
}
type checkMsg struct {
	err          error
	onOK, onAuth func() tea.Cmd
}
type actionMsg struct {
	text string
	err  error
	next func() tea.Cmd
}
type execDoneMsg struct{ err error }
type contMsg struct{ fn func() tea.Cmd }
type configEditedMsg struct{}

// ---------- Model ----------

type viewID int

const (
	vServers viewID = iota
	vContainers
)

type confirm struct {
	prompt string
	yes    func() tea.Cmd
}

type model struct {
	cfgPath string
	cfg     *Config

	view    viewID
	srvList list.Model
	srvDel  *srvDelegate
	ctList  list.Model
	ctDel   *ctDelegate

	cur         *Server
	node        string
	all         []Container
	onlyRunning bool
	loadSeq     int

	spin   spinner.Model
	busy   string
	msg    string
	msgErr bool
	ask    *confirm
}

func newList(d list.ItemDelegate, title, item string, short, full []key.Binding) list.Model {
	l := list.New(nil, d, 0, 0)
	l.Title = title
	l.Styles.Title = stTitle
	l.SetStatusBarItemName(item, item)
	l.KeyMap.Quit.SetEnabled(false)
	l.KeyMap.ForceQuit.SetEnabled(false)
	l.FilterInput.Prompt = "Filter: "
	l.AdditionalShortHelpKeys = func() []key.Binding { return short }
	l.AdditionalFullHelpKeys = func() []key.Binding { return full }
	return l
}

func newModel(cfgPath string, cfg *Config, start *Server) *model {
	m := &model{cfgPath: cfgPath, cfg: cfg, srvDel: &srvDelegate{}, ctDel: &ctDelegate{}}
	m.srvList = newList(m.srvDel, "pmx · Proxmox Server", "Server",
		[]key.Binding{keys.Open, keys.HostSSH},
		[]key.Binding{keys.Open, keys.HostSSH, keys.Ping, keys.Edit})
	m.ctList = newList(m.ctDel, "Container", "Container",
		[]key.Binding{keys.Enter, keys.CtSSH, keys.Back},
		[]key.Binding{keys.Enter, keys.CtSSH, keys.Host, keys.Start, keys.Stop, keys.Reboot,
			keys.Copy, keys.Running, keys.Refresh, keys.Back})
	m.spin = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(stSel))
	m.setServerItems()
	if start == nil && len(cfg.Servers) == 1 {
		start = &cfg.Servers[0]
	}
	m.cur = start
	return m
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.pingAll()}
	if m.cur != nil {
		cmds = append(cmds, m.openServer(m.cur))
	}
	return tea.Batch(cmds...)
}

func (m *model) setServerItems() {
	items := make([]list.Item, len(m.cfg.Servers))
	m.srvDel.nameW, m.srvDel.hostW = 4, 4
	for i := range m.cfg.Servers {
		s := &m.cfg.Servers[i]
		items[i] = srvItem{srv: s}
		m.srvDel.nameW = max(m.srvDel.nameW, ansi.StringWidth(s.Name))
		m.srvDel.hostW = max(m.srvDel.hostW, len(s.Target())+6)
	}
	m.srvList.SetItems(items)
}

func (m *model) pingAll() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.cfg.Servers {
		s := m.cfg.Servers[i]
		cmds = append(cmds, func() tea.Msg {
			t := time.Now()
			c, err := net.DialTimeout("tcp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), 2*time.Second)
			if err != nil {
				var dnsErr *net.DNSError
				if errors.As(err, &dnsErr) {
					return pingMsg{name: s.Name, state: pingUnknown} // evtl. ssh-config-Alias
				}
				return pingMsg{name: s.Name, state: pingOffline}
			}
			c.Close()
			return pingMsg{name: s.Name, state: pingOnline, lat: time.Since(t)}
		})
	}
	return tea.Batch(cmds...)
}

func (m *model) activeList() *list.Model {
	if m.view == vServers {
		return &m.srvList
	}
	return &m.ctList
}

func (m *model) selectedCt() (Container, bool) {
	it, ok := m.ctList.SelectedItem().(ctItem)
	return it.Container, ok
}

func (m *model) setMsg(s string, isErr bool) { m.msg, m.msgErr = s, isErr }
func (m *model) setErr(err error)            { m.setMsg(err.Error(), true) }

func (m *model) askf(yes func() tea.Cmd, format string, a ...any) {
	m.ask = &confirm{prompt: fmt.Sprintf(format, a...), yes: yes}
}

func (m *model) startBusy(text string, cmd tea.Cmd) tea.Cmd {
	was := m.busy != ""
	m.busy = text
	if was {
		return cmd
	}
	return tea.Batch(cmd, m.spin.Tick)
}

// ---------- Update ----------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.srvList.SetSize(msg.Width, msg.Height-1)
		m.ctList.SetSize(msg.Width, msg.Height-1)
		return m, nil

	case spinner.TickMsg:
		if m.busy == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case pingMsg:
		for i, it := range m.srvList.Items() {
			if si := it.(srvItem); si.srv.Name == msg.name {
				si.state, si.lat = msg.state, msg.lat
				m.srvList.SetItem(i, si)
			}
		}
		return m, nil

	case ctsMsg:
		if msg.seq != m.loadSeq {
			return m, nil
		}
		m.busy = ""
		if msg.err != nil {
			if isAuthErr(msg.err) {
				return m, m.offerHostKey(msg.srv, m.reload)
			}
			m.setErr(msg.err)
			return m, nil
		}
		m.node, m.all = msg.node, msg.cts
		return m, m.applyContainers()

	case checkMsg:
		m.busy = ""
		switch {
		case msg.err == nil:
			return m, msg.onOK()
		case isAuthErr(msg.err) && msg.onAuth != nil:
			return m, msg.onAuth()
		}
		m.setErr(msg.err)
		return m, nil

	case actionMsg:
		m.busy = ""
		if msg.err != nil {
			m.setErr(msg.err)
			return m, nil
		}
		if msg.text != "" {
			m.setMsg(msg.text, false)
		}
		if msg.next != nil {
			return m, msg.next()
		}
		return m, nil

	case contMsg:
		return m, msg.fn()

	case execDoneMsg:
		if msg.err != nil {
			var ee *exec.ExitError
			if !errors.As(msg.err, &ee) {
				m.setErr(msg.err)
			} else if ee.ExitCode() == 255 {
				m.setMsg("Verbindung fehlgeschlagen (Exit 255)", true)
			}
		}
		if m.view == vContainers && m.cur != nil {
			return m, m.reload()
		}
		return m, nil

	case configEditedMsg:
		cfg, err := LoadConfig(m.cfgPath)
		if err != nil {
			m.setErr(err)
			return m, nil
		}
		m.cfg, m.cur, m.view = cfg, nil, vServers
		m.setServerItems()
		m.setMsg("Config neu geladen", false)
		return m, m.pingAll()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.ask != nil {
			a := m.ask
			m.ask = nil
			switch msg.String() {
			case "y", "Y", "j", "J":
				return m, a.yes()
			}
			m.setMsg("abgebrochen", false)
			return m, nil
		}
		if m.activeList().FilterState() != list.Filtering {
			m.msg = ""
			if key.Matches(msg, keys.Quit) {
				return m, tea.Quit
			}
			var cmd tea.Cmd
			var handled bool
			if m.view == vServers {
				cmd, handled = m.serverKeys(msg)
			} else {
				cmd, handled = m.containerKeys(msg)
			}
			if handled {
				return m, cmd
			}
		}
	}

	var cmd tea.Cmd
	if m.view == vServers {
		m.srvList, cmd = m.srvList.Update(msg)
	} else {
		m.ctList, cmd = m.ctList.Update(msg)
	}
	return m, cmd
}

func (m *model) isBusyAction(msg tea.KeyMsg) bool {
	if m.busy == "" {
		return false
	}
	return key.Matches(msg, keys.Enter, keys.CtSSH, keys.Refresh, keys.Start, keys.Stop, keys.Reboot, keys.Host, keys.Edit)
}

func (m *model) serverKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.isBusyAction(msg) {
		return nil, true
	}
	it, ok := m.srvList.SelectedItem().(srvItem)
	switch {
	case key.Matches(msg, keys.Open):
		if ok {
			return m.openServer(it.srv), true
		}
	case key.Matches(msg, keys.HostSSH):
		if ok {
			return m.hostShell(it.srv), true
		}
	case key.Matches(msg, keys.Ping):
		for i, li := range m.srvList.Items() {
			si := li.(srvItem)
			si.state = pingUnknown
			m.srvList.SetItem(i, si)
		}
		return m.pingAll(), true
	case key.Matches(msg, keys.Edit):
		return m.editConfig(), true
	}
	return nil, false
}

func (m *model) containerKeys(msg tea.KeyMsg) (tea.Cmd, bool) {
	if key.Matches(msg, keys.Back) && m.ctList.FilterState() == list.Unfiltered {
		m.view = vServers
		m.loadSeq++ // laufende Ladevorgänge verwerfen
		m.busy = ""
		return nil, true
	}
	if m.isBusyAction(msg) {
		return nil, true
	}
	c, ok := m.selectedCt()
	switch {
	case key.Matches(msg, keys.Refresh):
		return m.reload(), true
	case key.Matches(msg, keys.Host):
		return m.hostShell(m.cur), true
	case key.Matches(msg, keys.Running):
		m.onlyRunning = !m.onlyRunning
		return m.applyContainers(), true
	}
	if !ok {
		return nil, false
	}
	switch {
	case key.Matches(msg, keys.Enter):
		return m.enterCt(c), true
	case key.Matches(msg, keys.CtSSH):
		return m.sshCt(c), true
	case key.Matches(msg, keys.Start):
		if c.Running() {
			m.setMsg(fmt.Sprintf("CT %d läuft bereits", c.ID), false)
			return nil, true
		}
		return m.power(c, "start"), true
	case key.Matches(msg, keys.Stop):
		if !c.Running() {
			m.setMsg(fmt.Sprintf("CT %d läuft nicht", c.ID), false)
			return nil, true
		}
		m.askf(func() tea.Cmd { return m.power(c, "shutdown") }, "CT %d (%s) herunterfahren?", c.ID, c.Name)
		return nil, true
	case key.Matches(msg, keys.Reboot):
		if !c.Running() {
			m.setMsg(fmt.Sprintf("CT %d läuft nicht", c.ID), false)
			return nil, true
		}
		m.askf(func() tea.Cmd { return m.power(c, "reboot") }, "CT %d (%s) neu starten?", c.ID, c.Name)
		return nil, true
	case key.Matches(msg, keys.Copy):
		ip := c.PrimaryIP()
		if ip == "" {
			m.setMsg("keine IP bekannt", true)
			return nil, true
		}
		if err := copyToClipboard(ip); err != nil {
			m.setErr(err)
		} else {
			m.setMsg(ip+" kopiert", false)
		}
		return nil, true
	}
	return nil, false
}

// ---------- Aktionen ----------

func (m *model) openServer(s *Server) tea.Cmd {
	if m.cur != s || m.all == nil {
		m.all, m.node = nil, ""
		m.ctList.SetItems(nil)
	}
	m.cur = s
	m.view = vContainers
	m.ctList.ResetFilter()
	m.updateCtTitle()
	return m.reload()
}

func (m *model) reload() tea.Cmd {
	m.loadSeq++
	seq, s := m.loadSeq, m.cur
	return m.startBusy("lade Container von "+s.Name+" …", func() tea.Msg {
		node, cts, err := s.ListContainers()
		return ctsMsg{seq: seq, srv: s, node: node, cts: cts, err: err}
	})
}

func (m *model) updateCtTitle() {
	if m.cur == nil {
		return
	}
	t := m.cur.Name
	if m.node != "" && m.node != m.cur.Name {
		t += " (" + m.node + ")"
	}
	if m.all != nil {
		run := 0
		for _, c := range m.all {
			if c.Running() {
				run++
			}
		}
		t += fmt.Sprintf(" · %d/%d laufend", run, len(m.all))
	}
	if m.onlyRunning {
		t += " · nur laufende"
	}
	m.ctList.Title = t
}

func (m *model) applyContainers() tea.Cmd {
	selID := -1
	if c, ok := m.selectedCt(); ok {
		selID = c.ID
	}
	d := m.ctDel
	d.idW, d.nameW, d.ipW = 3, 4, 2
	var items []list.Item
	for _, c := range m.all {
		if m.onlyRunning && !c.Running() {
			continue
		}
		items = append(items, ctItem{c})
		d.idW = max(d.idW, len(strconv.Itoa(c.ID)))
		d.nameW = min(max(d.nameW, ansi.StringWidth(c.Name)), 32)
		d.ipW = min(max(d.ipW, len(strings.Join(c.IPs, ", "))), 42)
	}
	cmd := m.ctList.SetItems(items)
	if m.ctList.FilterState() == list.Unfiltered {
		for i, it := range items {
			if it.(ctItem).ID == selID {
				m.ctList.Select(i)
				break
			}
		}
	}
	m.updateCtTitle()
	return cmd
}

func (m *model) enterCt(c Container) tea.Cmd {
	s := m.cur
	enter := func() tea.Cmd {
		return execShell(s.EnterCmd(c.ID), fmt.Sprintf("pct enter %d (%s) auf %s", c.ID, c.Name, s.Name))
	}
	if c.Running() {
		return enter()
	}
	m.askf(func() tea.Cmd {
		return m.startBusy(fmt.Sprintf("starte CT %d …", c.ID), func() tea.Msg {
			return actionMsg{err: s.Pct("start", c.ID), next: enter}
		})
	}, "CT %d (%s) ist gestoppt – starten und betreten?", c.ID, c.Name)
	return nil
}

func (m *model) sshCt(c Container) tea.Cmd {
	s := m.cur
	if !c.Running() {
		m.setMsg(fmt.Sprintf("CT %d läuft nicht – mit s starten", c.ID), true)
		return nil
	}
	ip := c.PrimaryIP()
	if ip == "" {
		m.setMsg(fmt.Sprintf("keine IP für CT %d bekannt – enter nutzt pct enter", c.ID), true)
		return nil
	}
	target := s.ContainerUser + "@" + ip
	open := func() tea.Cmd {
		return execShell(s.ContainerShellCmd(ip), fmt.Sprintf("ssh %s (CT %d %s)", target, c.ID, c.Name))
	}
	return m.startBusy("prüfe SSH-Zugang "+target+" …", func() tea.Msg {
		return checkMsg{
			err:    s.CheckContainerSSH(ip),
			onOK:   open,
			onAuth: func() tea.Cmd { return m.offerCtKey(s, c, target, open) },
		}
	})
}

func (m *model) hostShell(s *Server) tea.Cmd {
	open := func() tea.Cmd { return execShell(s.HostShellCmd(), "ssh "+s.Target()) }
	return m.startBusy("prüfe SSH-Zugang "+s.Target()+" …", func() tea.Msg {
		return checkMsg{
			err:    s.CheckHost(),
			onOK:   open,
			onAuth: func() tea.Cmd { return m.offerHostKey(s, open) },
		}
	})
}

func (m *model) power(c Container, action string) tea.Cmd {
	s := m.cur
	labels := map[string][2]string{
		"start":    {"starte", "gestartet"},
		"shutdown": {"fahre herunter:", "heruntergefahren"},
		"reboot":   {"starte neu:", "neu gestartet"},
	}[action]
	return m.startBusy(fmt.Sprintf("%s CT %d (%s) …", labels[0], c.ID, c.Name), func() tea.Msg {
		return actionMsg{text: fmt.Sprintf("CT %d %s", c.ID, labels[1]), err: s.Pct(action, c.ID), next: m.reload}
	})
}

func (m *model) offerKeygen(pub string, after func() tea.Cmd) tea.Cmd {
	m.askf(func() tea.Cmd {
		return tea.Exec(&bannerExec{cmd: KeygenCmd(pub), banner: "neuen SSH-Key erzeugen", pauseAny: true},
			func(err error) tea.Msg {
				if err != nil {
					return actionMsg{err: fmt.Errorf("ssh-keygen: %w", err)}
				}
				return contMsg{after}
			})
	}, "Kein lokaler SSH-Key gefunden – neuen Key %s erzeugen?", tildify(pub))
	return nil
}

func (m *model) offerHostKey(s *Server, after func() tea.Cmd) tea.Cmd {
	pub, ok := s.PubKeyPath()
	if !ok {
		return m.offerKeygen(pub, func() tea.Cmd { return m.offerHostKey(s, after) })
	}
	m.askf(func() tea.Cmd {
		return tea.Exec(&bannerExec{cmd: s.CopyIDCmd(pub), banner: "ssh-copy-id → " + s.Target(), pauseAny: true},
			func(err error) tea.Msg {
				if err != nil {
					return actionMsg{err: fmt.Errorf("ssh-copy-id fehlgeschlagen: %w", err)}
				}
				return contMsg{after}
			})
	}, "Kein SSH-Key auf %s hinterlegt – jetzt mit ssh-copy-id hinterlegen?", s.Target())
	return nil
}

func (m *model) offerCtKey(s *Server, c Container, target string, after func() tea.Cmd) tea.Cmd {
	pub, ok := s.PubKeyPath()
	if !ok {
		return m.offerKeygen(pub, func() tea.Cmd { return m.offerCtKey(s, c, target, after) })
	}
	m.askf(func() tea.Cmd {
		return m.startBusy(fmt.Sprintf("hinterlege Key in CT %d …", c.ID), func() tea.Msg {
			data, err := os.ReadFile(pub)
			if err == nil {
				err = s.PushContainerKey(c.ID, s.ContainerUser, strings.TrimSpace(string(data)))
			}
			return actionMsg{text: "SSH-Key hinterlegt", err: err, next: after}
		})
	}, "Kein SSH-Key für %s (CT %d) hinterlegt – jetzt per pct hinterlegen?", target, c.ID)
	return nil
}

func (m *model) editConfig() tea.Cmd {
	ed := first(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	cmd := exec.Command("sh", "-c", ed+` "$1"`, "sh", m.cfgPath)
	return tea.Exec(&bannerExec{cmd: cmd}, func(error) tea.Msg { return configEditedMsg{} })
}

// ---------- View ----------

func (m *model) View() string {
	return m.activeList().View() + "\n" + ansi.Truncate(m.statusLine(), m.activeList().Width(), "…")
}

func (m *model) statusLine() string {
	switch {
	case m.ask != nil:
		return stYellow.Render(" ? "+m.ask.prompt) + stDim.Render("  [y/N]")
	case m.busy != "":
		return " " + m.spin.View() + " " + m.busy
	case m.msg != "" && m.msgErr:
		return stRed.Render(" ✗ " + m.msg)
	case m.msg != "":
		return stGreen.Render(" ✓ " + m.msg)
	}
	return ""
}

// ---------- Exec-Hilfen ----------

// bannerExec zeigt vor dem Start eine Info-Zeile und pausiert bei Fehlern,
// damit Fehlermeldungen nicht sofort von der TUI überschrieben werden.
type bannerExec struct {
	cmd      *exec.Cmd
	banner   string
	pauseAny bool
}

func (b *bannerExec) SetStdin(r io.Reader)  { b.cmd.Stdin = r }
func (b *bannerExec) SetStdout(w io.Writer) { b.cmd.Stdout = w }
func (b *bannerExec) SetStderr(w io.Writer) { b.cmd.Stderr = w }
func (b *bannerExec) Run() error {
	out := b.cmd.Stdout
	if out == nil {
		out = os.Stdout
	}
	if b.banner != "" {
		fmt.Fprintf(out, "\033[2m── pmx: %s  (exit / ctrl+d → zurück zu pmx)\033[0m\n", b.banner)
	}
	err := b.cmd.Run()
	var ee *exec.ExitError
	if err != nil && (b.pauseAny || !errors.As(err, &ee) || ee.ExitCode() == 255) {
		fmt.Fprintf(out, "\n\033[31m── pmx: %v – Enter drücken …\033[0m", err)
		in := b.cmd.Stdin
		if in == nil {
			in = os.Stdin
		}
		buf := make([]byte, 1)
		for {
			if n, e := in.Read(buf); e != nil || (n == 1 && (buf[0] == '\n' || buf[0] == '\r')) {
				break
			}
		}
	}
	return err
}

func execShell(cmd *exec.Cmd, banner string) tea.Cmd {
	return tea.Exec(&bannerExec{cmd: cmd, banner: banner}, func(err error) tea.Msg { return execDoneMsg{err} })
}

func copyToClipboard(s string) error {
	try := func(name string, args ...string) bool {
		p, err := exec.LookPath(name)
		if err != nil {
			return false
		}
		c := exec.Command(p, args...)
		c.Stdin = strings.NewReader(s)
		return c.Run() == nil
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" && try("wl-copy") {
		return nil
	}
	if os.Getenv("DISPLAY") != "" && (try("xclip", "-selection", "clipboard") || try("xsel", "-ib")) {
		return nil
	}
	if try("pbcopy") {
		return nil
	}
	// Fallback: OSC 52 – funktioniert in den meisten Terminals, auch über SSH.
	_, err := fmt.Fprintf(os.Stdout, "\033]52;c;%s\a", base64.StdEncoding.EncodeToString([]byte(s)))
	return err
}
