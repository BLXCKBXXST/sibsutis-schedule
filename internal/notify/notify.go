// Package notify — отправка сообщений в Telegram. Сейчас один Sender —
// TelegramSender (Bot API), но интерфейс позволяет добавить тестовый
// stub или другую транспортную доставку без правок воркера.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Sender отправляет одно сообщение в чат. chatID — числовой Telegram chat_id.
// Реализация должна сама обработать таймауты и rate-limit'ы Bot API.
type Sender interface {
	Send(ctx context.Context, chatID int64, text string) error
}

// TelegramSender — production реализация Sender'а через Telegram Bot API.
// Использует одиночный *http.Client с таймаутом; повторные запросы лучше
// делать на уровне вызывающего кода, чтобы можно было считать дедуп.
type TelegramSender struct {
	Token  string
	Client *http.Client
}

// NewTelegramSender собирает sender с разумным HTTP-таймаутом.
func NewTelegramSender(token string) *TelegramSender {
	return &TelegramSender{
		Token:  token,
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Send посылает text в чат chatID. Markdown/HTML не применяется, чтобы не
// беспокоиться об экранировании. parse_mode опущен — Telegram отдаст
// сообщение как plain text.
func (t *TelegramSender) Send(ctx context.Context, chatID int64, text string) error {
	if t.Token == "" {
		return fmt.Errorf("telegram: пустой токен бота")
	}
	body, err := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
	if err != nil {
		return fmt.Errorf("telegram: marshal: %w", err)
	}
	url := "https://api.telegram.org/bot" + t.Token + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram: status %s", resp.Status)
	}
	return nil
}
