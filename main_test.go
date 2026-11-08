package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A private PATH symlink to this test binary stands in for journalctl. No real
// journal is read and no services, SSH sessions or Telegram requests are used.
func init() {
	if filepath.Base(os.Args[0]) == "journalctl" && os.Getenv("SSH_ALERTD_TEST_JOURNAL") == "eof" {
		os.Exit(0)
	}
}

func TestMainProcessHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "main-helper" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("ssh-alertd", flag.ExitOnError)
	os.Args = []string{os.Args[0], "-config", os.Getenv("SSH_ALERTD_TEST_CONFIG")}
	main()
}

func TestMainExitsNonzeroWhenJournalFollowerEnds(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "journalctl")); err != nil {
		t.Fatal(err)
	}
	const token = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	cfg := map[string]any{
		"log_source": map[string]any{"type": "journald"},
		"notifiers":  map[string]any{"telegram": map[string]any{"enabled": true, "bot_token": token, "chat_id": "1", "api_base": "http://127.0.0.1:1"}},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestMainProcessHelper$", "main-helper")
	cmd.Env = append(os.Environ(), "PATH="+dir, "SSH_ALERTD_TEST_JOURNAL=eof", "SSH_ALERTD_TEST_CONFIG="+path)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("main hung on follower EOF")
	}
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("exit=%v, output=%s", err, output)
	}
	if !strings.Contains(string(output), "monitor: log source") {
		t.Fatalf("main failed for wrong reason: %s", output)
	}
	if strings.Contains(string(output), token) {
		t.Fatal("main log exposed token")
	}
}
