package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"ssh-alertd/internal/config"
	"ssh-alertd/internal/event"
)

func TestJournalTrustedMetadata(t *testing.T) {
	valid := map[string]any{"MESSAGE": goodMessage, "_UID": "0", "_EXE": "/usr/sbin/sshd", "SYSLOG_IDENTIFIER": "sshd"}
	cases := []struct {
		name, field string
		value       any
		omit        bool
		ok          bool
	}{
		{name: "root sshd", ok: true},
		{name: "root sshd session", field: "_EXE", value: "/usr/lib/openssh/sshd-session", ok: true},
		{name: "root cwd no unit", field: "_CWD", value: "/root", ok: true},
		{name: "unprivileged spoofed identifier", field: "_UID", value: "1000"},
		{name: "missing UID", field: "_UID", omit: true},
		{name: "numeric UID", field: "_UID", value: 0},
		{name: "UID array", field: "_UID", value: []string{"0", "1000"}},
		{name: "null UID", field: "_UID", value: nil},
		{name: "root logger", field: "_EXE", value: "/usr/bin/logger"},
		{name: "root systemd cat", field: "_EXE", value: "/usr/bin/systemd-cat"},
		{name: "relative sshd", field: "_EXE", value: "sshd"},
		{name: "missing exe", field: "_EXE", omit: true},
		{name: "wrong basename", field: "_EXE", value: "/usr/sbin/sshd-fake"},
		{name: "exe array", field: "_EXE", value: []string{"/usr/sbin/sshd"}},
		{name: "missing message", field: "MESSAGE", omit: true},
		{name: "null message", field: "MESSAGE", value: nil},
		{name: "message array", field: "MESSAGE", value: []string{goodMessage}},
		{name: "invalid user", field: "MESSAGE", value: "Invalid user " + goodMessage},
		{name: "nested envelope", field: "MESSAGE", value: "Jun 26 09:49:01 host sshd[123]: " + goodMessage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := make(map[string]any)
			for k, v := range valid {
				entry[k] = v
			}
			if tc.field != "" {
				if tc.omit {
					delete(entry, tc.field)
				} else {
					entry[tc.field] = tc.value
				}
			}
			data, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			msg, ok := journalMessage(string(data))
			if ok != tc.ok {
				t.Fatalf("accepted=%v, want %v", ok, tc.ok)
			}
			if ok && msg != goodMessage {
				t.Fatalf("wrong message %q", msg)
			}
		})
	}
	for _, line := range []string{"", "{broken SECRET", "{}", "null", "[]", `{"MESSAGE":5}`, `{} {}`} {
		if _, ok := journalMessage(line); ok {
			t.Errorf("accepted malformed journal record: %q", line)
		}
	}
}

func TestNewSourceCommandArguments(t *testing.T) {
	src, err := NewSource(config.LogSourceConfig{Type: config.SourceJournald})
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*commandSource)
	if s.cmd != "journalctl" || !s.journal || !reflect.DeepEqual(s.args, []string{"-f", "-n", "0", "-o", "json", "-t", "sshd", "-t", "sshd-session"}) {
		t.Fatalf("unsafe journal command: %+v", s)
	}
	src, err = NewSource(config.LogSourceConfig{Type: config.SourceFile, Path: "-unsafe-option"})
	if err != nil {
		t.Fatal(err)
	}
	s = src.(*commandSource)
	if !s.file || !reflect.DeepEqual(s.args, []string{"-F", "-n", "0", "--", "-unsafe-option"}) {
		t.Fatalf("unsafe tail command: %+v", s)
	}
}

// Run this same test binary as a controlled child. No journal/logger/SSH calls.
func TestSourceCommandHelper(t *testing.T) {
	args := os.Args
	if len(args) < 2 || args[len(args)-2] != "source-helper" {
		return
	}
	switch args[len(args)-1] {
	case "eof":
		return
	case "exit":
		os.Exit(7)
	case "block":
		time.Sleep(30 * time.Second)
	case "event-block":
		fmt.Println(goodMessage)
		time.Sleep(30 * time.Second)
	case "closed-stdout":
		_ = os.Stdout.Close()
		time.Sleep(30 * time.Second)
	case "scanner":
		fmt.Print(strings.Repeat("PRIVATE-SOURCE-TOKEN", 100000))
		time.Sleep(30 * time.Second)
	case "file":
		fmt.Println(goodMessage) // Bare MESSAGE is not a file-source envelope.
		fmt.Println("bogus prefix " + goodMessage)
		fmt.Println("Jun 26 09:49:01 host logger[123]: " + goodMessage)
		fmt.Println("Jun 26 09:49:01 host sshd[123]: Invalid user " + goodMessage)
		fmt.Println("Jun 26 09:49:01 host sshd[123]: " + goodMessage)
	case "journal":
		fmt.Println(`{malformed PRIVATE-SOURCE-TOKEN`)
		fmt.Println(`{"_UID":"1000","_EXE":"/usr/sbin/sshd","SYSLOG_IDENTIFIER":"sshd","MESSAGE":"` + goodMessage + `"}`)
		fmt.Println(`{"_UID":"0","_EXE":"/usr/bin/logger","SYSLOG_IDENTIFIER":"sshd","MESSAGE":"` + goodMessage + `"}`)
		fmt.Println(`{"_UID":"0","_EXE":"/usr/sbin/sshd","MESSAGE":"` + goodMessage + `"}`)
	}
	os.Exit(0)
}

func helperSource(t *testing.T, mode string) *commandSource {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &commandSource{name: "test-helper", cmd: exe, args: []string{"-test.run=^TestSourceCommandHelper$", "source-helper", mode}}
}

func TestMonitorCommandTermination(t *testing.T) {
	for _, mode := range []string{"eof", "exit", "closed-stdout", "scanner"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			src := helperSource(t, mode)
			var logs bytes.Buffer
			err := New(src, "host", log.New(&logs, "", 0)).Run(ctx, func(context.Context, event.LoginEvent) { t.Fatal("unexpected event") })
			if err == nil {
				t.Fatal("source failure/EOF returned nil")
			}
			if ctx.Err() != nil {
				t.Fatal("source failure hung until timeout")
			}
			if strings.Contains(err.Error()+logs.String(), "PRIVATE-SOURCE-TOKEN") {
				t.Fatal("source leaked raw input")
			}
			if src.Err() == nil {
				t.Fatal("source did not preserve failure")
			}
		})
	}
}

func TestMonitorContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := helperSource(t, "event-block")
	done := make(chan error, 1)
	ready := make(chan struct{})
	defer cancel()
	go func() {
		done <- New(src, "host", log.New(io.Discard, "", 0)).Run(ctx, func(context.Context, event.LoginEvent) { close(ready) })
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("monitor never received child event")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancellation returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation hung")
	}
}

func TestCommandSourceCancellationClosesStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := helperSource(t, "block")
	lines, err := src.Lines(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-lines:
		if ok {
			t.Fatal("unexpected child output")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child stream not closed after cancellation")
	}
	if err := src.Err(); err != nil {
		t.Fatalf("cancellation stored failure %v", err)
	}
}

func TestCommandSourcesFilterBeforeParse(t *testing.T) {
	for _, mode := range []string{"file", "journal"} {
		t.Run(mode, func(t *testing.T) {
			src := helperSource(t, mode)
			src.file, src.journal = mode == "file", mode == "journal"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			count := 0
			err := New(src, "host", log.New(io.Discard, "", 0)).Run(ctx, func(_ context.Context, e event.LoginEvent) {
				count++
				if e.Username != "root" {
					t.Errorf("wrong event %+v", e)
				}
			})
			if err == nil || ctx.Err() != nil {
				t.Fatalf("unexpected termination: %v / %v", err, ctx.Err())
			}
			if count != 1 {
				t.Fatalf("got %d accepted events, want 1", count)
			}
		})
	}
}

type closedSource struct{}

func (closedSource) Name() string { return "closed" }
func (closedSource) Lines(context.Context) (<-chan string, error) {
	c := make(chan string)
	close(c)
	return c, nil
}
func TestMonitorEOFWithoutSourceErrorInterface(t *testing.T) {
	err := New(closedSource{}, "host", log.New(io.Discard, "", 0)).Run(context.Background(), func(context.Context, event.LoginEvent) {})
	if !errors.Is(err, ErrSourceClosed) {
		t.Fatalf("EOF returned %v", err)
	}
}
