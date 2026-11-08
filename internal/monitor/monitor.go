// Locally modified for setup-vps security build; see SECURITY_PATCHES.md.
// Package monitor reads sshd log output and emits successful-login events.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ssh-alertd/internal/event"
)

// Match a complete MESSAGE, never a substring of an invalid-user diagnostic.
var acceptedRE = regexp.MustCompile(`^Accepted ([A-Za-z0-9][A-Za-z0-9@._/-]*) for (\S+) from (\S+) port ([0-9]+) ssh2(?:: [^\r\n]*)?$`)

// Only known structural syslog envelopes may precede an sshd MESSAGE. The
// timestamp is also parsed below. A random prefix ending in sshd is not enough.
var syslogRE = regexp.MustCompile(`^([A-Z][a-z]{2} [ 0-9][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2}|[0-9]{4}-[0-9]{2}-[0-9]{2}T\S+) ([^\s]+) sshd(?:-session)?\[[1-9][0-9]*\]: (.+)$`)

var ErrSourceClosed = errors.New("log source ended before cancellation")

type Handler func(ctx context.Context, e event.LoginEvent)

type Monitor struct {
	source   Source
	hostname string
	logger   *log.Logger
	now      func() time.Time
}

func New(source Source, hostname string, logger *log.Logger) *Monitor {
	if logger == nil {
		logger = log.Default()
	}
	return &Monitor{source: source, hostname: hostname, logger: logger, now: time.Now}
}

// Run returns an error for EOF, scanner failure or command exit before context
// cancellation. Cancellation is normal shutdown and always returns nil.
func (m *Monitor) Run(ctx context.Context, handler Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines, err := m.source.Lines(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	m.logger.Printf("monitor started on source %s (host=%s)", m.source.Name(), m.hostname)
	for {
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-lines:
			if ctx.Err() != nil {
				return nil
			}
			if !ok {
				if s, ok := m.source.(interface{ Err() error }); ok {
					if err := s.Err(); err != nil {
						return fmt.Errorf("log source failed: %w", err)
					}
				}
				return ErrSourceClosed
			}
			if e, ok := m.parse(line); ok {
				m.logger.Printf("detected %s", e)
				handler(ctx, e)
			}
		}
	}
}

func syslogMessage(line string) (string, bool) {
	if strings.IndexFunc(line, unicode.IsControl) >= 0 {
		return "", false
	}
	m := syslogRE.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	layout := "Jan _2 15:04:05"
	if strings.Contains(m[1], "T") {
		layout = time.RFC3339Nano
	}
	if _, err := time.Parse(layout, m[1]); err != nil {
		return "", false
	}
	return m[3], true
}

func (m *Monitor) parse(line string) (event.LoginEvent, bool) {
	if strings.IndexFunc(line, unicode.IsControl) >= 0 {
		return event.LoginEvent{}, false
	}
	if !strings.HasPrefix(line, "Accepted ") {
		var ok bool
		line, ok = syslogMessage(line)
		if !ok {
			return event.LoginEvent{}, false
		}
	}
	match := acceptedRE.FindStringSubmatch(line)
	if match == nil || net.ParseIP(match[3]) == nil {
		return event.LoginEvent{}, false
	}
	port, err := strconv.Atoi(match[4])
	if err != nil || port < 1 || port > 65535 {
		return event.LoginEvent{}, false
	}
	return event.LoginEvent{Method: match[1], Username: match[2], IP: match[3], Port: match[4], Hostname: m.hostname, Time: m.now()}, true
}
