package main

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"testing"
)

func TestVersionProcessHelper(t *testing.T) {
	if os.Getenv("SSH_ALERTD_VERSION_HELPER") != "1" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("ssh-alertd", flag.ExitOnError)
	// A nonexistent config proves -version needs neither config nor credentials.
	os.Args = []string{"ssh-alertd", "-config", "/nonexistent/ssh-alertd-fixture.json", "-version"}
	main()
	os.Exit(0)
}

func TestVersionDoesNotReadConfigOrStartMonitoring(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestVersionProcessHelper$")
	cmd.Env = append(os.Environ(), "SSH_ALERTD_VERSION_HELPER=1", "PATH=/nonexistent")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v: %s", err, raw)
	}
	var info map[string]string
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("not JSON: %v: %s", err, raw)
	}
	if len(info) != 4 || info["version"] != "dev" || info["commit"] != "unknown" || info["source_state"] != "unknown" || info["go_version"] == "" {
		t.Fatalf("unexpected identity: %v", info)
	}
}
