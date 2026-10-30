package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPortFromListen(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{":7863", 7863},
		{"127.0.0.1:8080", 8080},
		{"0.0.0.0:9000", 9000},
		{"[::1]:9100", 9100},
		{"", defaultPort},
		{"bad", defaultPort},
		{":0", defaultPort},
		{":99999", defaultPort},
	}
	for _, c := range cases {
		if got := portFromListen(c.in); got != c.want {
			t.Errorf("portFromListen(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestHostFromListen(t *testing.T) {
	cases := []struct{ in, want string }{
		{":7863", ""},
		{"127.0.0.1:8080", "127.0.0.1"},
		{"0.0.0.0:9000", "0.0.0.0"},
		{"[::1]:9100", "::1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := hostFromListen(c.in); got != c.want {
			t.Errorf("hostFromListen(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPortBusyDetectsLoopbackListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !portBusy("", port) {
		t.Fatalf("portBusy should detect listener on 127.0.0.1:%d", port)
	}
}

func TestLoadConfigMissingUsesDefaults(t *testing.T) {
	cfg, has, err := loadConfig(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if has {
		t.Fatal("hasCfg should be false for missing file")
	}
	if cfg.Listen != ":7863" || cfg.AuthDir != "./auths" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigPartial(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"listen":"127.0.0.1:18000","auth_dir":"/tmp/creds"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, has, err := loadConfig(p)
	if err != nil || !has {
		t.Fatalf("loadConfig: has=%v err=%v", has, err)
	}
	if cfg.Listen != "127.0.0.1:18000" || cfg.AuthDir != "/tmp/creds" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if got := portFromListen(cfg.Listen); got != 18000 {
		t.Fatalf("port = %d, want 18000", got)
	}
}

func TestCountJSONIgnoresNonJSONAndDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.json", "b.JSON", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "nested.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := countJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("countJSON = %d, want 2", got)
	}
}

func TestIsSourceDir(t *testing.T) {
	dir := t.TempDir()
	if isSourceDir(dir) {
		t.Fatal("empty dir should not be a source dir")
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isSourceDir(dir) {
		t.Fatal("dir with go.mod should be a source dir")
	}
}
