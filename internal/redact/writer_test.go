package redact

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func fullyEncoded(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&b, "%%%02X", s[i])
	}
	return b.String()
}

func TestWriterRedactsKnownTokensAndEncodings(t *testing.T) {
	token := "123456789:AbCdEfGhIjKlMnOpQrStUvWxYz012345678"
	for _, variant := range []string{token, url.QueryEscape(token), url.PathEscape(token), strings.ReplaceAll(token, ":", "%3a"), fullyEncoded(token), strings.ToLower(fullyEncoded(token)), "123456789%3AAbCd%45fGhIjKlMnOpQrStUvWxYz012345678"} {
		var dst bytes.Buffer
		w := NewWriter(&dst, token)
		input := "failed POST https://api.example/bot" + variant + "/sendMessage\n"
		n, err := w.Write([]byte(input))
		if err != nil || n != len(input) {
			t.Fatalf("Write=%d,%v", n, err)
		}
		if dst.String() != "failed POST https://api.example/bot[REDACTED]/sendMessage\n" {
			t.Fatalf("not redacted: %q", dst.String())
		}
	}
}

func TestWriterGenericBotTokenAndAdditionalSecrets(t *testing.T) {
	for _, token := range []string{"987654321:ABCDEFGHIJKLMNOPQRSTuvwx", "987654321%3aABCDEFGHIJKLMNOPQRSTuvwx", "smtp-password-not-a-bot"} {
		var dst bytes.Buffer
		logger := log.New(NewWriter(&dst, "smtp-password-not-a-bot", ""), "", 0)
		logger.Printf("failure: %s", token)
		if dst.String() != "failure: [REDACTED]\n" {
			t.Fatalf("token not redacted: %q", dst.String())
		}
	}
}

func TestWriterUnicodeAndQuerySpace(t *testing.T) {
	secret := "пароль с пробелом"
	for _, variant := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), fullyEncoded(secret)} {
		var dst bytes.Buffer
		_, err := NewWriter(&dst, secret).Write([]byte(variant))
		if err != nil || dst.String() != "[REDACTED]" {
			t.Fatalf("unicode secret not redacted: %q / %v", dst.String(), err)
		}
	}
}

func TestWriterConcurrentLogRecords(t *testing.T) {
	var dst bytes.Buffer
	w := NewWriter(&dst, "private-secret")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = w.Write([]byte("private-secret\n")) }()
	}
	wg.Wait()
	if dst.String() != strings.Repeat("[REDACTED]\n", 20) {
		t.Fatalf("incorrect concurrent output: %q", dst.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 1, nil }

func TestWriterPropagatesWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		dst  io.Writer
		want error
	}{{failingWriter{}, io.ErrClosedPipe}, {shortWriter{}, io.ErrShortWrite}} {
		if _, err := NewWriter(tc.dst).Write([]byte("message")); !errors.Is(err, tc.want) {
			t.Fatalf("Write returned %v, want %v", err, tc.want)
		}
	}
}
