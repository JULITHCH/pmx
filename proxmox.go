package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Container struct {
	ID     int
	Status string // RUNNING, STOPPED, FROZEN, ...
	Name   string
	IPs    []string
	Tags   []string
	Cores  string
	Memory string // MB
}

func (c Container) Running() bool { return c.Status == "RUNNING" }

// PrimaryIP prefers IPv4.
func (c Container) PrimaryIP() string {
	for _, ip := range c.IPs {
		if !strings.Contains(ip, ":") {
			return ip
		}
	}
	if len(c.IPs) > 0 {
		return c.IPs[0]
	}
	return ""
}

// listScript collects everything in a single SSH round trip. Status and IPs
// come straight from lxc-info (much faster than pct status / pct exec).
// For stopped containers, static IPs from the config are shown.
const listScript = `d="${PMX_PVE_DIR:-/etc/pve}"
[ -d "$d" ] || { echo "$d not found – is this a Proxmox host?" >&2; exit 2; }
printf '#node\t%s\n' "$(hostname)"
for f in "$d"/lxc/*.conf; do
  [ -e "$f" ] || continue
  id=${f##*/}; id=${id%.conf}
  cfg=$(awk '/^\[/{exit} {print}' "$f")
  get() { printf '%s\n' "$cfg" | sed -n "s/^$1: *//p" | head -n1; }
  state=$(lxc-info -n "$id" -sH 2>/dev/null)
  [ -n "$state" ] || state=UNKNOWN
  ips=""
  if [ "$state" = RUNNING ]; then
    ips=$(lxc-info -n "$id" -iH 2>/dev/null | grep -v -e '^127\.' -e '^::1$' -e '^fe80' | paste -sd, -)
  fi
  if [ -z "$ips" ]; then
    ips=$(printf '%s\n' "$cfg" | grep -o 'ip6*=[^,]*' | sed 's/^ip6*=//; s#/.*##' | grep -v -e '^dhcp$' -e '^manual$' -e '^auto$' | paste -sd, -)
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$state" "$(get hostname)" "$ips" "$(get tags)" "$(get cores)" "$(get memory)"
done
`

func (s *Server) ListContainers() (string, []Container, error) {
	out, err := s.Run(listScript)
	if err != nil {
		return "", nil, err
	}
	return parseList(out)
}

func parseList(out string) (string, []Container, error) {
	var node string
	var cts []Container
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if n, ok := strings.CutPrefix(line, "#node\t"); ok {
			node = n
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		c := Container{ID: id, Status: f[1], Name: f[2], Cores: f[5], Memory: f[6]}
		if c.Name == "" {
			c.Name = fmt.Sprintf("CT%d", id)
		}
		c.IPs = splitAny(f[3], ",")
		c.Tags = splitAny(f[4], ";, ")
		cts = append(cts, c)
	}
	sort.Slice(cts, func(i, j int) bool { return cts[i].ID < cts[j].ID })
	return node, cts, nil
}

func splitAny(s, seps string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(seps, r) })
}

// Pct runs start/shutdown/reboot etc.
func (s *Server) Pct(action string, id int) error {
	_, err := s.Run(fmt.Sprintf("pct %s %d\n", action, id))
	return err
}

// FindContainer matches by ID, exact name or unique substring.
func FindContainer(cts []Container, ref string) (Container, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		for _, c := range cts {
			if c.ID == id {
				return c, nil
			}
		}
		return Container{}, fmt.Errorf("no container with ID %d", id)
	}
	r := strings.ToLower(ref)
	var m []Container
	for _, c := range cts {
		n := strings.ToLower(c.Name)
		if n == r {
			return c, nil
		}
		if strings.Contains(n, r) {
			m = append(m, c)
		}
	}
	switch len(m) {
	case 1:
		return m[0], nil
	case 0:
		return Container{}, fmt.Errorf("no container %q found", ref)
	}
	var names []string
	for _, c := range m {
		names = append(names, fmt.Sprintf("%d:%s", c.ID, c.Name))
	}
	return Container{}, fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(names, ", "))
}
