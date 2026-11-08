package notifier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ssh-alertd/internal/redact"
)

const privateTestToken = "123456789:AbCdEfGhIjKlMnOpQrStUvWxYz012345678"

func assertNoTelegramSecrets(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{privateTestToken, url.QueryEscape(privateTestToken), url.PathEscape(privateTestToken), strings.ReplaceAll(privateTestToken, ":", "%3a")} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret exposed in %q", text)
		}
	}
}

func testTelegramEndpoint(t *testing.T, endpoint string) *Telegram {
	t.Helper()
	tg, err := NewTelegram(TelegramOptions{BotToken: privateTestToken, ChatID: "1", APIBase: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	tg.client.Transport = &http.Transport{Proxy: nil}
	t.Cleanup(tg.client.CloseIdleConnections)
	return tg
}

func checkRealTransportFailure(t *testing.T, tg *Telegram, want string) {
	t.Helper()
	err := tg.Send(context.Background(), testEvent())
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want failure containing %q", err, want)
	}
	assertNoTelegramSecrets(t, err.Error())
	if strings.Contains(err.Error(), tg.apiBase) || strings.Contains(err.Error(), "/sendMessage") {
		t.Fatalf("full URL exposed: %v", err)
	}
	var logs bytes.Buffer
	logger := log.New(redact.NewWriter(&logs, privateTestToken), "", 0)
	NewDispatcher(logger, time.Second, tg).Dispatch(context.Background(), testEvent())
	if !strings.Contains(logs.String(), "failed") {
		t.Fatalf("expected dispatcher failure, got %q", logs.String())
	}
	assertNoTelegramSecrets(t, logs.String())
}

func TestTelegramRealConnectionRefusedDoesNotLeak(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{privateTestToken, url.QueryEscape(privateTestToken)} {
		tg := testTelegramEndpoint(t, endpoint)
		tg.botToken = token
		checkRealTransportFailure(t, tg, "transport or TLS")
	}
}

func TestTelegramRealTLSFailureDoesNotLeak(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS connection must not reach handler")
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	checkRealTransportFailure(t, testTelegramEndpoint(t, srv.URL), "transport or TLS")
}

func TestTelegramRealClientTimeoutDoesNotLeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	tg := testTelegramEndpoint(t, srv.URL)
	tg.client.Timeout = 20 * time.Millisecond
	checkRealTransportFailure(t, tg, "timed out")
	err := tg.Send(context.Background(), testEvent())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout identity lost: %v", err)
	}
}

func TestTelegramResponseCannotEchoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, "%s %s %s", privateTestToken, url.QueryEscape(privateTestToken), r.URL.String())
	}))
	defer srv.Close()
	tg := testTelegramEndpoint(t, srv.URL)
	checkRealTransportFailure(t, tg, "status 401")
}

func TestTelegramDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1) }))
			defer target.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/bot"+privateTestToken, status)
			}))
			defer srv.Close()
			tg := testTelegramEndpoint(t, srv.URL)
			err := tg.Send(context.Background(), testEvent())
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Fatalf("redirect accepted: %v", err)
			}
			if count.Load() != 0 {
				t.Fatalf("redirect leaked request to target (%d requests)", count.Load())
			}
			assertNoTelegramSecrets(t, err.Error())
		})
	}
}

func TestTelegramBuildRequestFailureDoesNotLeak(t *testing.T) {
	tg := testTelegramEndpoint(t, "http://[invalid-"+privateTestToken)
	checkRealTransportFailure(t, tg, "build telegram request failed")
}

func TestTelegramSuccessfulRealRequest(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/bot"+privateTestToken+"/sendMessage" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("invalid Telegram request")
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	if err := testTelegramEndpoint(t, srv.URL).Send(context.Background(), testEvent()); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatalf("got %d requests, want 1", count.Load())
	}
}

func TestTelegramDefaultHTMLEscapesAllFields(t *testing.T) {
	e := testEvent()
	e.Username, e.Hostname, e.IP, e.Port, e.Method = "<user>", "<host>", "<ip>", "<port>", "<method>"
	msg := defaultTelegramHTML(e)
	for _, field := range []string{"user", "host", "ip", "port", "method"} {
		if strings.Contains(msg, "<"+field+">") || !strings.Contains(msg, "&lt;"+field+"&gt;") {
			t.Errorf("unescaped field %s in %q", field, msg)
		}
	}
}
