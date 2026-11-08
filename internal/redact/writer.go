// Package redact provides a log-record writer that removes configured secrets
// and recognizable Telegram bot tokens. Pass whole log records (as log.Logger
// does); this is not a streaming protocol sanitizer across separate writes.
package redact

import (
	"io"
	"regexp"
	"strings"
	"sync"
)

var botTokenRE = regexp.MustCompile(`[0-9]+(?::|%3[aA])[A-Za-z0-9_-]{20,}`)

type Writer struct {
	mu      sync.Mutex
	dst     io.Writer
	secrets []*regexp.Regexp
}

func NewWriter(dst io.Writer, secrets ...string) *Writer {
	w := &Writer{dst: dst}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		// Each rune may be raw or UTF-8 percent-encoded. ASCII bot tokens
		// additionally allow arbitrary mixed encoding and hex letter case.
		var pattern strings.Builder
		const hex = "0123456789ABCDEF"
		for _, r := range secret {
			raw := string(r)
			pattern.WriteString("(?:" + regexp.QuoteMeta(raw) + "|(?i:")
			for _, b := range []byte(raw) {
				pattern.WriteString("%" + string([]byte{hex[b>>4], hex[b&15]}))
			}
			pattern.WriteString(")")
			if r == ' ' {
				pattern.WriteString(`|\+`)
			}
			pattern.WriteString(")")
		}
		w.secrets = append(w.secrets, regexp.MustCompile(pattern.String()))
	}
	return w
}

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := string(p)
	for _, secret := range w.secrets {
		s = secret.ReplaceAllString(s, "[REDACTED]")
	}
	s = botTokenRE.ReplaceAllString(s, "[REDACTED]")
	n, err := io.WriteString(w.dst, s)
	if err != nil {
		return 0, err
	}
	if n != len(s) {
		return 0, io.ErrShortWrite
	}
	return len(p), nil
}
