package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Führt listScript lokal gegen ein nachgebautes /etc/pve und ein Fake-lxc-info aus.
func TestListScript(t *testing.T) {
	dir := t.TempDir()
	pve := filepath.Join(dir, "pve")
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(filepath.Join(pve, "lxc"), 0o755)
	os.MkdirAll(bin, 0o755)

	os.WriteFile(filepath.Join(pve, "lxc", "101.conf"), []byte(`arch: amd64
cores: 2
hostname: web
memory: 1024
net0: name=eth0,bridge=vmbr0,ip=dhcp,type=veth
tags: prod;web

[snap1]
hostname: old-name
`), 0o644)
	os.WriteFile(filepath.Join(pve, "lxc", "120.conf"), []byte(`hostname: db
memory: 2048
net0: name=eth0,bridge=vmbr0,ip=10.0.0.20/24,gw=10.0.0.1,ip6=dhcp
`), 0o644)
	os.WriteFile(filepath.Join(bin, "lxc-info"), []byte(`#!/bin/sh
case "$*" in
  "-n 101 -sH") echo RUNNING ;;
  "-n 101 -iH") printf '10.0.0.5\n127.0.0.1\nfe80::1\n2001:db8::5\n' ;;
  *-sH) echo STOPPED ;;
esac
`), 0o755)

	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(listScript)
	cmd.Env = append(os.Environ(), "PMX_PVE_DIR="+pve, "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	_, cts, _ := parseList(string(out))
	if len(cts) != 2 {
		t.Fatalf("want 2 containers, got %d: %s", len(cts), out)
	}
	web, db := cts[0], cts[1]
	if web.ID != 101 || web.Name != "web" || !web.Running() || web.Cores != "2" || web.Memory != "1024" {
		t.Errorf("web falsch: %+v", web)
	}
	if strings.Join(web.IPs, ",") != "10.0.0.5,2001:db8::5" || strings.Join(web.Tags, ",") != "prod,web" {
		t.Errorf("web IPs/Tags falsch: %+v", web)
	}
	if db.Running() || strings.Join(db.IPs, ",") != "10.0.0.20" || db.PrimaryIP() != "10.0.0.20" {
		t.Errorf("db falsch: %+v", db)
	}
}

// Prüft das verschachtelte Quoting (ssh → sh -s → sh -c) mit bösartigem Key-Kommentar.
func TestPushKeyQuoting(t *testing.T) {
	home := t.TempDir()
	key := `ssh-ed25519 AAAAC3Nza'Weird "comment" $HOME`
	inner := `k="$2"; h="` + home + `"
mkdir -p "$h/.ssh" && touch "$h/.ssh/authorized_keys"
grep -qxF "$k" "$h/.ssh/authorized_keys" || printf '%s\n' "$k" >> "$h/.ssh/authorized_keys"`
	script := "sh -c " + shq(inner) + " sh " + shq("root") + " " + shq(key) + "\n"
	for range 2 { // zweimal: muss idempotent sein
		cmd := exec.Command("sh", "-s")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	got, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if string(got) != key+"\n" {
		t.Fatalf("authorized_keys = %q", got)
	}
}

func TestFindContainer(t *testing.T) {
	cts := []Container{{ID: 101, Name: "web"}, {ID: 102, Name: "webproxy"}, {ID: 200, Name: "db"}}
	if c, err := FindContainer(cts, "web"); err != nil || c.ID != 101 {
		t.Errorf("exakter Name: %v %v", c, err)
	}
	if c, err := FindContainer(cts, "prox"); err != nil || c.ID != 102 {
		t.Errorf("Teilstring: %v %v", c, err)
	}
	if _, err := FindContainer(cts, "we"); err == nil {
		t.Error("mehrdeutig erwartet")
	}
}
