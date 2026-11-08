package main

import (
	"os"
	"strings"
	"testing"
)

func TestDeploymentServiceKeepsPrivilegeSeparationAndRestart(t *testing.T) {
	raw, err := os.ReadFile("deploy/ssh-alertd.service")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string][]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = append(values[key], value)
		}
	}
	for key, want := range map[string]string{
		"User": "ssh-alertd", "Group": "ssh-alertd",
		"SupplementaryGroups": "systemd-journal", "Restart": "always",
		"NoNewPrivileges": "yes", "ProtectSystem": "strict",
		"ProtectHome": "yes", "PrivateTmp": "yes",
	} {
		if got := values[key]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v; want one %q", key, got, want)
		}
	}
}

func TestDeploymentConfigIsRootOwnedAndServiceReadOnly(t *testing.T) {
	raw, err := os.ReadFile("deploy/ssh-alertd.tmpfiles")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"/etc/ssh-alertd":             "0750",
		"/etc/ssh-alertd/config.json": "0640",
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 6 || seen[fields[1]] || want[fields[1]] == "" {
			t.Fatalf("unexpected tmpfiles directive: %q", line)
		}
		seen[fields[1]] = true
		if fields[0] != "z" || fields[2] != want[fields[1]] || fields[3] != "root" || fields[4] != "ssh-alertd" || fields[5] != "-" {
			t.Errorf("unsafe config ownership/mode: %q", line)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("missing protected paths: %v", seen)
	}
}
