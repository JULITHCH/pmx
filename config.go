package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Server is a Proxmox host from the config.
type Server struct {
	Name          string `toml:"name"`
	Host          string `toml:"host"`
	User          string `toml:"user"`
	Port          int    `toml:"port"`
	IdentityFile  string `toml:"identity_file"`
	Sudo          bool   `toml:"sudo"`
	ContainerUser string `toml:"container_user"`
	Jump          *bool  `toml:"jump"`
}

type Defaults struct {
	User          string `toml:"user"`
	Port          int    `toml:"port"`
	IdentityFile  string `toml:"identity_file"`
	Sudo          bool   `toml:"sudo"`
	ContainerUser string `toml:"container_user"`
	Jump          *bool  `toml:"jump"`
}

type Config struct {
	Defaults Defaults `toml:"defaults"`
	Servers  []Server `toml:"server"`
}

const exampleConfig = `# pmx – fast access to Proxmox LXC containers
#
# Each [[server]] is a Proxmox node. In a cluster, add every node separately
# (pct enter only works on the node that hosts the container).

[defaults]
user = "root"                 # SSH user on the Proxmox host
# port = 22
# identity_file = "~/.ssh/id_ed25519"
container_user = "root"       # user for ctrl+s (SSH directly into a container)
jump = true                   # tunnel container SSH through the Proxmox host
# sudo = false                # run Proxmox commands via sudo (if user != root)

[[server]]
name = "pve1"
host = "192.168.1.10"

# [[server]]
# name = "pve2"
# host = "pve2.example.lan"
# user = "admin"
# sudo = true
# port = 2222
`

func defaultConfigPath() string {
	if p := os.Getenv("PMX_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(homeDir(), ".config")
	}
	return filepath.Join(dir, "pmx", "config.toml")
}

func writeExampleConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(exampleConfig), 0o600)
}

func LoadConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	d := cfg.Defaults
	seen := map[string]bool{}
	for i := range cfg.Servers {
		s := &cfg.Servers[i]
		if s.Name == "" {
			s.Name = s.Host
		}
		if s.Host == "" {
			return nil, fmt.Errorf("config: server %q has no host", s.Name)
		}
		if seen[strings.ToLower(s.Name)] {
			return nil, fmt.Errorf("config: duplicate server name %q", s.Name)
		}
		seen[strings.ToLower(s.Name)] = true
		s.User = first(s.User, d.User, "root")
		s.ContainerUser = first(s.ContainerUser, d.ContainerUser, "root")
		s.IdentityFile = expandHome(first(s.IdentityFile, d.IdentityFile))
		if s.Port == 0 {
			s.Port = d.Port
		}
		if s.Port == 0 {
			s.Port = 22
		}
		if !s.Sudo {
			s.Sudo = d.Sudo
		}
		if s.Jump == nil {
			s.Jump = d.Jump
		}
		if s.Jump == nil {
			t := true
			s.Jump = &t
		}
	}
	return &cfg, nil
}

// Find looks up a server by name (exact match, then unique prefix).
func (c *Config) Find(name string) (*Server, error) {
	n := strings.ToLower(name)
	var matches []*Server
	for i := range c.Servers {
		s := &c.Servers[i]
		if strings.ToLower(s.Name) == n {
			return s, nil
		}
		if strings.HasPrefix(strings.ToLower(s.Name), n) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("no server %q in the config", name)
	}
	var names []string
	for _, s := range matches {
		names = append(names, s.Name)
	}
	return nil, fmt.Errorf("%q is ambiguous: %s", name, strings.Join(names, ", "))
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func expandHome(p string) string {
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

func tildify(p string) string {
	h := homeDir()
	if strings.HasPrefix(p, h+"/") {
		return "~" + p[len(h):]
	}
	return p
}
