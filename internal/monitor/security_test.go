package monitor

import (
	"strings"
	"testing"
)

const goodMessage = "Accepted publickey for root from 203.0.113.5 port 2222 ssh2: ED25519 SHA256:test"

func TestParseRejectsSpoofedOrMalformedSuccess(t *testing.T) {
	m := New(nil, "host", nil)
	cases := []string{
		"Invalid user " + goodMessage,
		"Failed password for " + goodMessage,
		"Disconnected from invalid user " + goodMessage,
		"Disconnecting invalid user " + goodMessage,
		"error: " + goodMessage,
		" " + goodMessage,
		"Accepted password for invalid user root from 203.0.113.5 port 22 ssh2",
		"Accepted password for root from example.com port 22 ssh2",
		"Accepted password for root from 999.0.0.1 port 22 ssh2",
		"Accepted password for root from 203.0.113.5 port 0 ssh2",
		"Accepted password for root from 203.0.113.5 port 65536 ssh2",
		"Accepted password for root from 203.0.113.5 port 99999999999999999999999 ssh2",
		"Accepted password for root from 203.0.113.5 port -1 ssh2",
		"Accepted password for root from 203.0.113.5 port 22junk ssh2",
		"Accepted password for root from 203.0.113.5 port 22",
		"Accepted password for root from 203.0.113.5 port 22 ssh2 evil",
		"Accepted password for root from [2001:db8::1] port 22 ssh2",
		"Accepted password for root from fe80::1%eth0 port 22 ssh2",
		goodMessage + "\n" + goodMessage,
		goodMessage + "\r",
		goodMessage + "\x00",
		"Jun 26 09:49:01 host logger[123]: " + goodMessage,
		"Jun 26 09:49:01 host sshd[123]: Invalid user " + goodMessage,
		"Invalid user Jun 26 09:49:01 host sshd[123]: " + goodMessage,
		"Jun 32 99:49:01 host sshd[123]: " + goodMessage,
		"2026-13-26T09:49:01Z host sshd[123]: " + goodMessage,
		"arbitrary host sshd[123]: " + goodMessage,
		"Jun 26 09:49:01 host sshd[0]: " + goodMessage,
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			if _, ok := m.parse(line); ok {
				t.Fatalf("accepted non-success: %q", line)
			}
		})
	}
}

func TestParseIPv6AndStructuredSyslog(t *testing.T) {
	m := New(nil, "host", nil)
	for _, line := range []string{
		"Accepted keyboard-interactive/pam for Accepted from 2001:db8::1 port 1 ssh2",
		"Accepted password for alice from ::1 port 65535 ssh2",
		"Jun  6 09:49:01 host sshd-session[123]: " + goodMessage,
		"2026-06-26T09:49:01.123456+03:00 host sshd[123]: " + goodMessage,
		"2026-06-26T09:49:01Z host sshd-session[123]: " + goodMessage,
	} {
		if _, ok := m.parse(line); !ok {
			t.Errorf("rejected legitimate success %q", line)
		}
	}
	// A username containing the marker cannot promote an unsuccessful line.
	for _, prefix := range []string{"Failed password for ", "Invalid user ", "Disconnected from ", "Disconnecting "} {
		if _, ok := m.parse(prefix + strings.ReplaceAll(goodMessage, "root", "Accepted")); ok {
			t.Errorf("promoted a failed message with prefix %q", prefix)
		}
	}
}
