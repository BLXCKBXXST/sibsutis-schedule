// Package subs хранит подписки Telegram-чатов на изменения расписания target'ов.
//
// Подписки лежат в одном JSON-файле в каталоге данных. Формат:
//
//	{
//	  "12345678": [{"type":"student","query":"ИКС-531"}, ...],
//	  "98765432": [{"type":"room","query":"1-101"}]
//	}
//
// API потокобезопасен; каждое изменение пишется на диск через temp+rename.
package subs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Store — подписки по чатам.
type Store struct {
	path string
	mu   sync.Mutex
	data map[int64][]model.Target
}

// New открывает (создавая при необходимости) файл подписок в dataDir.
func New(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, fmt.Errorf("dataDir пуст")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("создание каталога %s: %w", dataDir, err)
	}
	s := &Store{path: filepath.Join(dataDir, "subscriptions.json"), data: map[int64][]model.Target{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load читает файл с диска. Отсутствие файла — не ошибка.
func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("чтение %s: %w", s.path, err)
	}
	// Ключи в JSON могут быть только строками, преобразуем.
	raw := map[string][]model.Target{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("разбор %s: %w", s.path, err)
		}
	}
	s.data = make(map[int64][]model.Target, len(raw))
	for k, v := range raw {
		var id int64
		if _, err := fmt.Sscan(k, &id); err != nil {
			continue // пропускаем мусорные ключи
		}
		s.data[id] = v
	}
	return nil
}

// save атомарно записывает текущее состояние на диск.
// Вызывать только под s.mu.
func (s *Store) save() error {
	raw := make(map[string][]model.Target, len(s.data))
	for k, v := range s.data {
		raw[fmt.Sprintf("%d", k)] = v
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("сериализация подписок: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("запись %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("переименование %s -> %s: %w", tmp, s.path, err)
	}
	return nil
}

// Add подписывает chatID на target. Возвращает added=false, если подписка уже была.
func (s *Store) Add(chatID int64, t model.Target) (added bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := t.Key()
	for _, existing := range s.data[chatID] {
		if existing.Key() == key {
			return false, nil
		}
	}
	s.data[chatID] = append(s.data[chatID], t)
	if err := s.save(); err != nil {
		// Откатим in-memory изменение, чтобы состояние совпадало с диском.
		s.data[chatID] = s.data[chatID][:len(s.data[chatID])-1]
		return false, err
	}
	return true, nil
}

// Remove убирает подписку chatID на target. Возвращает removed=false, если её не было.
func (s *Store) Remove(chatID int64, t model.Target) (removed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := t.Key()
	list := s.data[chatID]
	idx := -1
	for i, existing := range list {
		if existing.Key() == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false, nil
	}
	prev := append([]model.Target(nil), list...)
	s.data[chatID] = append(list[:idx], list[idx+1:]...)
	if len(s.data[chatID]) == 0 {
		delete(s.data, chatID)
	}
	if err := s.save(); err != nil {
		s.data[chatID] = prev // откат
		return false, err
	}
	return true, nil
}

// List возвращает подписки конкретного чата (копия среза).
func (s *Store) List(chatID int64) []model.Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.data[chatID]
	out := make([]model.Target, len(src))
	copy(out, src)
	return out
}

// Subscribers возвращает chatID, подписанных на target.
func (s *Store) Subscribers(t model.Target) []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := t.Key()
	var out []int64
	for chatID, ts := range s.data {
		for _, existing := range ts {
			if existing.Key() == key {
				out = append(out, chatID)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// UniqueTargets возвращает все уникальные target'ы, на которые есть подписки.
// Нужно фоновому циклу обновлений: какие target'ы обновлять.
func (s *Store) UniqueTargets() []model.Target {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]model.Target{}
	for _, ts := range s.data {
		for _, t := range ts {
			seen[t.Key()] = t
		}
	}
	out := make([]model.Target, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
