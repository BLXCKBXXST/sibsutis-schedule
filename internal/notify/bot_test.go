package notify

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// stubSubscriber — простая in-memory реализация Subscriber для тестов.
type stubSubscriber struct {
	mu      sync.Mutex
	entries map[string]struct {
		target model.Target
		chats  map[int64]bool
	}
}

func newStubSubscriber() *stubSubscriber {
	return &stubSubscriber{entries: map[string]struct {
		target model.Target
		chats  map[int64]bool
	}{}}
}

func (s *stubSubscriber) Subscribe(t model.Target, chatID int64, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := t.Key()
	e, ok := s.entries[k]
	if !ok {
		e.target = t
		e.chats = map[int64]bool{}
	}
	if e.chats[chatID] {
		return false, nil
	}
	e.chats[chatID] = true
	s.entries[k] = e
	return true, nil
}

func (s *stubSubscriber) Unsubscribe(t model.Target, chatID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := t.Key()
	e, ok := s.entries[k]
	if !ok || !e.chats[chatID] {
		return false, nil
	}
	delete(e.chats, chatID)
	s.entries[k] = e
	return true, nil
}

func (s *stubSubscriber) TargetsForChat(chatID int64) []model.Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.Target
	for _, e := range s.entries {
		if e.chats[chatID] {
			out = append(out, e.target)
		}
	}
	return out
}

// hasSub — хэлпер для тестов: подписан ли chatID на target.
func (s *stubSubscriber) hasSub(t model.Target, chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[t.Key()].chats[chatID]
}

// captureSender запоминает, что отправляли.
type captureSender struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureSender) Send(_ context.Context, _ int64, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, text)
	return nil
}

func TestStartTokenRoundtrip(t *testing.T) {
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}
	tok := EncodeStartToken(target)

	got, err := decodeStartToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != target.Type || got.Query != target.Query {
		t.Errorf("roundtrip = %+v, want %+v", got, target)
	}
}

func TestHandleStartWithToken(t *testing.T) {
	sub := newStubSubscriber()
	snd := &captureSender{}
	b := &Bot{Reg: sub, Sender: snd}
	target := model.Target{Type: model.TypeStudent, Query: "ИКС-531"}
	tok := EncodeStartToken(target)

	b.handleStart(context.Background(), 42, tok)

	if !sub.hasSub(target, 42) {
		t.Error("chat 42 не подписался на ИКС-531")
	}
	if len(snd.msgs) != 1 {
		t.Fatalf("ожидался 1 ответ, было %d", len(snd.msgs))
	}
}

func TestHandleStartWithoutToken(t *testing.T) {
	sub := newStubSubscriber()
	snd := &captureSender{}
	b := &Bot{Reg: sub, Sender: snd}

	b.handleStart(context.Background(), 42, "")
	if len(snd.msgs) != 1 {
		t.Fatalf("ожидался 1 ответ, было %d", len(snd.msgs))
	}
	if len(sub.entries) != 0 {
		t.Error("без токена подписка не должна была создаться")
	}
}

func TestHandleListAndUnsubscribe(t *testing.T) {
	sub := newStubSubscriber()
	snd := &captureSender{}
	b := &Bot{Reg: sub, Sender: snd}
	_, _ = sub.Subscribe(model.Target{Type: model.TypeStudent, Query: "X"}, 1, time.Time{})
	_, _ = sub.Subscribe(model.Target{Type: model.TypeStudent, Query: "Y"}, 1, time.Time{})

	b.handleList(context.Background(), 1)
	last := snd.msgs[len(snd.msgs)-1]
	if !strings.Contains(last, "X") || !strings.Contains(last, "Y") {
		t.Errorf("/list должен содержать X и Y: %q", last)
	}

	b.handleUnsubscribe(context.Background(), 1, "")
	if len(sub.TargetsForChat(1)) != 0 {
		t.Error("после /unsubscribe должно остаться 0 подписок")
	}
}
