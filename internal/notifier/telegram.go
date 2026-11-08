// Locally modified for setup-vps security build; see SECURITY_PATCHES.md.
package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	htmltemplate "html/template"
	"net"
	"net/http"
	"strings"
	texttemplate "text/template"
	"time"

	"ssh-alertd/internal/event"
)

// Telegram parse modes.
const (
	tgParseHTML       = "HTML"
	tgParseMarkdownV2 = "MarkdownV2"
	tgParseMarkdown   = "Markdown"
	tgParseNone       = "none" // plain text, no formatting
)

// TelegramOptions configures a Telegram notifier.
type TelegramOptions struct {
	BotToken string
	ChatID   string
	APIBase  string
	// MessageTemplate is an optional Go template for the message text. Empty
	// uses the built-in HTML format. Templates receive the login event
	// (.Username .IP .Port .Method .Hostname .Time).
	MessageTemplate string
	// ParseMode is "HTML" (default), "MarkdownV2", "Markdown" or "none". It
	// applies to a custom MessageTemplate; the built-in format is always HTML.
	ParseMode string
}

// Telegram delivers alerts through the Telegram Bot API (sendMessage).
type Telegram struct {
	botToken  string
	chatID    string
	apiBase   string
	parseMode string // value sent to Telegram ("" = plain)
	client    *http.Client

	render renderFunc
}

// NewTelegram builds a Telegram notifier and compiles any custom template up
// front so template errors surface at startup. apiBase may be empty (defaults
// to the public Telegram endpoint).
func NewTelegram(o TelegramOptions) (*Telegram, error) {
	apiBase := o.APIBase
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}

	render, reqParseMode, err := buildTelegramRenderer(o.MessageTemplate, o.ParseMode)
	if err != nil {
		return nil, fmt.Errorf("message_template: %w", err)
	}

	return &Telegram{
		botToken:  o.BotToken,
		chatID:    o.ChatID,
		apiBase:   strings.TrimRight(apiBase, "/"),
		parseMode: reqParseMode,
		client: &http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		render: render,
	}, nil
}

// Name implements Notifier.
func (t *Telegram) Name() string { return "telegram" }

// buildTelegramRenderer compiles the message renderer and resolves the parse
// mode actually sent to Telegram. An empty template uses the built-in HTML
// format regardless of parseMode. For a custom template, "HTML" uses
// html/template so event fields are auto-escaped; other modes use text/template
// (the author is responsible for any required escaping).
func buildTelegramRenderer(tmpl, parseMode string) (renderFunc, string, error) {
	if tmpl == "" {
		return func(e event.LoginEvent) (string, error) {
			return defaultTelegramHTML(e), nil
		}, tgParseHTML, nil
	}

	switch parseMode {
	case tgParseHTML, "":
		t, err := htmltemplate.New("telegram").Parse(tmpl)
		if err != nil {
			return nil, "", err
		}
		return func(e event.LoginEvent) (string, error) {
			var b strings.Builder
			if err := t.Execute(&b, e); err != nil {
				return "", err
			}
			return b.String(), nil
		}, tgParseHTML, nil
	case tgParseMarkdownV2, tgParseMarkdown:
		t, err := texttemplate.New("telegram").Parse(tmpl)
		if err != nil {
			return nil, "", err
		}
		return func(e event.LoginEvent) (string, error) {
			var b strings.Builder
			if err := t.Execute(&b, e); err != nil {
				return "", err
			}
			return b.String(), nil
		}, parseMode, nil
	case tgParseNone:
		t, err := texttemplate.New("telegram").Parse(tmpl)
		if err != nil {
			return nil, "", err
		}
		return func(e event.LoginEvent) (string, error) {
			var b strings.Builder
			if err := t.Execute(&b, e); err != nil {
				return "", err
			}
			return b.String(), nil
		}, "", nil
	default:
		return nil, "", fmt.Errorf("invalid parse_mode %q (want HTML, MarkdownV2, Markdown or none)", parseMode)
	}
}

// sendMessageRequest mirrors the subset of Telegram's sendMessage payload we use.
type sendMessageRequest struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode,omitempty"`
}

// Send implements Notifier by POSTing the rendered message.
func (t *Telegram) Send(ctx context.Context, e event.LoginEvent) error {
	text, err := t.render(e)
	if err != nil {
		return fmt.Errorf("render message: %w", err)
	}

	payload := sendMessageRequest{
		ChatID:    t.chatID,
		Text:      text,
		ParseMode: t.parseMode,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", t.apiBase, t.botToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errors.New("build telegram request failed")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		// net/http errors include the request URL (and the bot token).
		// Do not return/wrap arbitrary transport errors, even url.Error.Err:
		// proxy/TLS errors can include URLs and server-controlled content.
		if ctx.Err() != nil {
			return fmt.Errorf("send telegram request: %w", ctx.Err())
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			return fmt.Errorf("send telegram request timed out: %w", context.DeadlineExceeded)
		}
		return errors.New("send telegram request failed (transport or TLS error)")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Response bodies and status text may echo credentials. Log only the
		// numeric status; never include an untrusted response snippet.
		return fmt.Errorf("telegram api status %d", resp.StatusCode)
	}
	return nil
}

// defaultTelegramHTML renders the built-in Telegram HTML message. User-controlled
// fields are escaped to avoid breaking the markup or injecting tags.
func defaultTelegramHTML(e event.LoginEvent) string {
	esc := html.EscapeString
	return fmt.Sprintf(
		"🔐 <b>SSH Login Alert</b>\n\n"+
			"<b>Host:</b> <code>%s</code>\n"+
			"<b>User:</b> <code>%s</code>\n"+
			"<b>From IP:</b> <code>%s</code>\n"+
			"<b>Client Port:</b> <code>%s</code>\n"+
			"<b>Method:</b> <code>%s</code>\n"+
			"<b>Time:</b> <code>%s</code>",
		esc(e.Hostname), esc(e.Username), esc(e.IP), esc(e.Port), esc(e.Method),
		esc(e.Time.Format("2006-01-02 15:04:05 MST")),
	)
}
