package web

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/auth"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
)

const (
	suggestCacheTTL = 5 * time.Minute
	suggestLimit    = 15
	suggestMinQ     = 2 // минимум символов в запросе, чтобы дёргать сайт
	suggestCacheMax = 256
)

// suggestItem — одно совпадение в ответе /api/suggest.
type suggestItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type suggestEntry struct {
	items []suggestItem
	at    time.Time
}

// suggester держит авторизованный HTTP-клиент к my.sibsutis.ru и
// in-memory TTL-кэш ответов /api/suggest. Защищает от keypress-флуда:
// одинаковые запросы за TTL отвечают мгновенно и без обращения к сайту.
type suggester struct {
	cfg *config.Config

	authMu sync.Mutex
	client *http.Client

	cacheMu sync.Mutex
	cache   map[string]suggestEntry
}

func newSuggester(cfg *config.Config) *suggester {
	return &suggester{cfg: cfg, cache: map[string]suggestEntry{}}
}

// suggest возвращает до suggestLimit вариантов для type+q. type — это
// сегмент URL (group/teacher/room), q — уже обрезанная строка запроса.
func (s *suggester) suggest(typ, q string) ([]suggestItem, error) {
	tt, ok := urlTypeToTargetType(typ)
	if !ok {
		return nil, nil
	}
	key := typ + "|" + strings.ToLower(q)

	// Быстрая ветка из кэша.
	s.cacheMu.Lock()
	if e, ok := s.cache[key]; ok && time.Since(e.at) < suggestCacheTTL {
		items := e.items
		s.cacheMu.Unlock()
		return items, nil
	}
	s.cacheMu.Unlock()

	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	matches, _, err := resolve.Search(client, s.cfg.ScheduleURL, model.Target{Type: tt, Query: q})
	if err != nil {
		// Возможно сессия протухла — сбросим клиент, следующий запрос
		// перезайдёт.
		s.authMu.Lock()
		s.client = nil
		s.authMu.Unlock()
		return nil, err
	}

	items := make([]suggestItem, 0, len(matches))
	for i, m := range matches {
		if i >= suggestLimit {
			break
		}
		items = append(items, suggestItem{ID: m.ID, Text: m.Text})
	}

	s.cacheMu.Lock()
	if len(s.cache) >= suggestCacheMax {
		s.gcLocked()
	}
	s.cache[key] = suggestEntry{items: items, at: time.Now()}
	s.cacheMu.Unlock()
	return items, nil
}

// gcLocked удаляет истёкшие записи кэша. Вызывается под cacheMu.
func (s *suggester) gcLocked() {
	now := time.Now()
	for k, e := range s.cache {
		if now.Sub(e.at) >= suggestCacheTTL {
			delete(s.cache, k)
		}
	}
}

// getClient возвращает авторизованный *http.Client; перелогинивается лениво.
func (s *suggester) getClient() (*http.Client, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	c, err := auth.Login(s.cfg.AuthURL, s.cfg.Login, s.cfg.Password, schedule.DefaultHTTPTimeout)
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}
