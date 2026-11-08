// Locally modified for setup-vps security build; see SECURITY_PATCHES.md.
package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"ssh-alertd/internal/config"
)

// Source closes its channel on cancellation or underlying stream termination.
// Sources may expose Err() error, read after channel closure, for a safe reason.
type Source interface {
	Name() string
	Lines(ctx context.Context) (<-chan string, error)
}

func NewSource(cfg config.LogSourceConfig) (Source, error) {
	switch cfg.Type {
	case config.SourceJournald:
		return &commandSource{name: "journald", cmd: "journalctl", args: []string{"-f", "-n", "0", "-o", "json", "-t", "sshd", "-t", "sshd-session"}, journal: true}, nil
	case config.SourceFile:
		return &commandSource{name: "file:" + cfg.Path, cmd: "tail", args: []string{"-F", "-n", "0", "--", cfg.Path}, file: true}, nil
	default:
		return nil, fmt.Errorf("unsupported log source type %q", cfg.Type)
	}
}

// SYSLOG_IDENTIFIER is client-supplied. Only trusted journal credentials and
// executable metadata establish the origin. Do not require a particular unit
// or cwd: a legitimate root sshd can run independently of systemd.
func journalMessage(line string) (string, bool) {
	var entry struct {
		Message string `json:"MESSAGE"`
		UID     string `json:"_UID"`
		Exe     string `json:"_EXE"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return "", false
	}
	if entry.UID != "0" || !filepath.IsAbs(entry.Exe) {
		return "", false
	}
	switch filepath.Base(entry.Exe) {
	case "sshd", "sshd-session":
	default:
		return "", false
	}
	// Journal MESSAGE must be raw, never another syslog envelope.
	if !strings.HasPrefix(entry.Message, "Accepted ") {
		return "", false
	}
	return entry.Message, true
}

type commandSource struct {
	name    string
	cmd     string
	args    []string
	journal bool
	file    bool
	mu      sync.Mutex
	err     error
}

func (s *commandSource) Name() string { return s.name }
func (s *commandSource) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}
func (s *commandSource) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *commandSource) Lines(ctx context.Context) (<-chan string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, s.cmd, s.args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, errors.New("source stdout pipe failed")
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdout.Close()
		return nil, errors.New("source command could not start")
	}
	s.setError(nil)
	out := make(chan string)
	go func() {
		defer close(out)
		defer cancel()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			var ok bool
			if s.journal {
				line, ok = journalMessage(line)
				if !ok {
					continue
				}
			} else if s.file {
				line, ok = syslogMessage(line)
				if !ok {
					continue
				}
			}
			select {
			case out <- line:
			case <-ctx.Done():
				cancel()
				_ = stdout.Close()
				_ = cmd.Wait()
				return
			}
		}
		scanErr := scanner.Err()
		if scanErr != nil {
			// Stop the child before Wait: a child can continue writing after a
			// scanner error. Never log raw input or scanner/command stderr.
			cancel()
			_ = stdout.Close()
		}
		// EOF is also unexpected for followers. Terminate a child that closed
		// stdout but kept running, so Wait cannot hide EOF indefinitely.
		cancel()
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		switch {
		case scanErr != nil:
			s.setError(errors.New("source stream read failed"))
		case waitErr != nil:
			s.setError(errors.New("source command exited unsuccessfully"))
		default:
			s.setError(ErrSourceClosed)
		}
	}()
	return out, nil
}
