// Locally modified for setup-vps security build; see SECURITY_PATCHES.md.
// Command ssh-alertd watches sshd logs and sends an alert on every successful
// SSH login. Backends are pluggable; Telegram and SMTP are implemented today.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ssh-alertd/internal/config"
	"ssh-alertd/internal/monitor"
	"ssh-alertd/internal/notifier"
	"ssh-alertd/internal/redact"
)

func main() {
	configPath := flag.String("config", "/etc/ssh-alertd/config.json", "path to the JSON config file")
	flag.Parse()

	logger := log.New(redact.NewWriter(os.Stderr), "ssh-alertd ", log.LstdFlags|log.Lmsgprefix)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Fatalf("config: %v", err)
	}

	writer := redact.NewWriter(os.Stderr, cfg.Notifiers.Telegram.BotToken, cfg.Notifiers.SMTP.Password)
	logger.SetOutput(writer)
	log.SetOutput(writer)

	hostname := cfg.Hostname
	if hostname == "" {
		if h, err := os.Hostname(); err == nil {
			hostname = h
		} else {
			hostname = "unknown"
		}
	}

	notifiers, err := buildNotifiers(cfg, logger)
	if err != nil {
		logger.Fatalf("notifiers: %v", err)
	}
	if len(notifiers) == 0 {
		logger.Fatalf("no notifiers enabled; nothing to do")
	}
	dispatcher := notifier.NewDispatcher(logger, 10*time.Second, notifiers...)

	src, err := monitor.NewSource(cfg.LogSource)
	if err != nil {
		logger.Fatalf("log source: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mon := monitor.New(src, hostname, logger)
	if err := mon.Run(ctx, dispatcher.Dispatch); err != nil && err != context.Canceled {
		logger.Fatalf("monitor: %v", err)
	}
	logger.Printf("shutting down")
}

// buildNotifiers constructs the enabled notifier backends from config.
func buildNotifiers(cfg *config.Config, logger *log.Logger) ([]notifier.Notifier, error) {
	var ns []notifier.Notifier

	if cfg.Notifiers.Telegram.Enabled {
		t := cfg.Notifiers.Telegram

		// A message template file, when set, overrides the inline template.
		message := t.MessageTemplate
		if t.MessageTemplateFile != "" {
			data, err := os.ReadFile(t.MessageTemplateFile)
			if err != nil {
				return nil, fmt.Errorf("telegram message_template_file: %w", err)
			}
			message = string(data)
		}

		tg, err := notifier.NewTelegram(notifier.TelegramOptions{
			BotToken:        t.BotToken,
			ChatID:          t.ChatID,
			APIBase:         t.APIBase,
			MessageTemplate: message,
			ParseMode:       t.ParseMode,
		})
		if err != nil {
			return nil, fmt.Errorf("telegram: %w", err)
		}
		ns = append(ns, tg)
		logger.Printf("enabled notifier: telegram")
	}

	if cfg.Notifiers.SMTP.Enabled {
		s := cfg.Notifiers.SMTP

		// A body template file, when set, overrides the inline body template.
		body := s.BodyTemplate
		if s.BodyTemplateFile != "" {
			data, err := os.ReadFile(s.BodyTemplateFile)
			if err != nil {
				return nil, fmt.Errorf("smtp body_template_file: %w", err)
			}
			body = string(data)
		}

		sm, err := notifier.NewSMTP(notifier.SMTPOptions{
			Host:            s.Host,
			Port:            s.Port,
			Username:        s.Username,
			Password:        s.Password,
			From:            s.From,
			To:              s.To,
			Encryption:      s.Encryption,
			SubjectTemplate: s.SubjectTemplate,
			BodyTemplate:    body,
			HTML:            s.HTML,
		})
		if err != nil {
			return nil, fmt.Errorf("smtp: %w", err)
		}
		ns = append(ns, sm)
		logger.Printf("enabled notifier: smtp")
	}

	// Future backends (whatsapp, wecom, dingtalk, feishu) register here.

	return ns, nil
}
