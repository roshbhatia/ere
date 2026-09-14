package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roshbhatia/ere/internal/config"
)

func TestInitAndPluginInstallPreserveExistingFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("ERE_CONFIG", "")
	run := func(args ...string) error {
		cmd := NewRootCmd("test")
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		return cmd.Execute()
	}
	if err := run("init", "--provider", "docker"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners[0].DiskGB != 0 || cfg.Runners[0].Workspace != dir {
		t.Fatalf("init: %+v", cfg.Runners[0])
	}
	original, err := os.ReadFile("ere.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = run("init"); err == nil {
		t.Fatal("overwrote configuration")
	}
	current, _ := os.ReadFile("ere.yaml")
	if !bytes.Equal(original, current) {
		t.Fatal("configuration changed")
	}
	if err = run("plugin", "install", ".amp/plugins/ere"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(".amp/plugins/ere/settings.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), filepath.Join(dir, "ere.yaml")) {
		t.Fatalf("plugin did not bind project config: %s", data)
	}
	if _, err = os.Stat(".amp/plugins/ere/skills/runner-workflow/SKILL.md"); err != nil {
		t.Fatal(err)
	}
	if err = run("plugin", "install", ".amp/plugins/ere"); err == nil {
		t.Fatal("overwrote plugin")
	}
}
