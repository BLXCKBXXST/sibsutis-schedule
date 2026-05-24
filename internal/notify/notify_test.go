package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelegramSenderSendsJSON(t *testing.T) {
	var seen struct {
		path string
		body map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seen.body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := &TelegramSender{Token: "ABC123", Client: ts.Client()}
	// Подменяем URL: переписываем хост на test server. Делаем это
	// перехватом через Transport — проще, чем плодить новые поля.
	s.Client = &http.Client{
		Timeout: 5 * time.Second,
		Transport: rewriteTransport{base: http.DefaultTransport, host: strings.TrimPrefix(ts.URL, "http://")},
	}

	if err := s.Send(context.Background(), 42, "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(seen.path, "/botABC123/sendMessage") {
		t.Errorf("path = %q", seen.path)
	}
	if seen.body["text"] != "test" {
		t.Errorf("body.text = %v", seen.body["text"])
	}
	if cid, _ := seen.body["chat_id"].(float64); cid != 42 {
		t.Errorf("body.chat_id = %v, want 42", seen.body["chat_id"])
	}
}

type rewriteTransport struct {
	base http.RoundTripper
	host string
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = r.host
	return r.base.RoundTrip(req)
}

func TestTelegramSenderEmptyTokenFails(t *testing.T) {
	s := &TelegramSender{Token: ""}
	if err := s.Send(context.Background(), 1, "x"); err == nil {
		t.Error("ожидалась ошибка при пустом токене")
	}
}
