// Package watch — список target'ов, расписания которых нужно держать
// свежими в фоне. Каждый просмотр через веб добавляет target в Registry;
// фоновый Worker раз в час обходит список и форсирует обновление через
// schedule.Service. Target'ы, которые никто не смотрел больше TTL дней,
// выкидываются — иначе мы бесконечно бы дёргали my.sibsutis.ru.
package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Entry — одна запись в реестре. LastTouched обновляется при каждом
// просмотре и при каждом успешном фоновом обновлении.
type Entry struct {
	Type        model.TargetType `json:"type"`
	Query       string           `json:"query"`
	LastTouched time.Time        `json:"last_touched"`
	// Subscribers — Telegram chat_id-ы, подписанные на изменения этого
	// target'а. nil/пусто — подписок нет; запись всё равно может жить
	// в реестре для поддержания горячего кэша.
	Subscribers []int64 `json:"subscribers,omitempty"`
	// LastNotifiedVersion — ID версии из store, по которой в последний
	// раз была отправлена рассылка diff'а. Защита от дубликатов: после
	// очередного fetch рассылку шлём только если ID последней версии
	// в store отличается. Пусто — рассылок ещё не было.
	LastNotifiedVersion string `json:"last_notified_version,omitempty"`
}

// Target собирает Entry обратно в model.Target.
func (e Entry) Target() model.Target {
	return model.Target{Type: e.Type, Query: e.Query}
}

// fileShape — формат хранения на диске.
type fileShape struct {
	Entries []Entry `json:"entries"`
}

// Registry хранит реестр в памяти и синхронно сериализуется на диск
// при каждом изменении. Производительность здесь не критична: target'ов
// — десятки, изменения — единицы в минуту.
type Registry struct {
	path string
	mu   sync.RWMutex
	data map[string]Entry // key = model.Target.Key()
}

// Open читает существующий watch.json или создаёт пустой реестр.
// Каталог под path создаётся, если его нет.
func Open(path string) (*Registry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("watch: mkdir: %w", err)
	}
	r := &Registry{path: path, data: make(map[string]Entry)}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("watch: read %s: %w", path, err)
	}
	if len(b) == 0 {
		return r, nil
	}
	var shape fileShape
	if err := json.Unmarshal(b, &shape); err != nil {
		return nil, fmt.Errorf("watch: parse %s: %w", path, err)
	}
	for _, e := range shape.Entries {
		t := model.Target{Type: e.Type, Query: e.Query}
		r.data[t.Key()] = e
	}
	return r, nil
}

// Touch создаёт или обновляет запись для target'а — точку last_touched
// сдвигаем на now. Subscribers и LastNotifiedVersion сохраняются, если
// запись существовала. Возвращает true, если запись была новой.
func (r *Registry) Touch(t model.Target, now time.Time) (bool, error) {
	key := t.Key()
	r.mu.Lock()
	prev, existed := r.data[key]
	e := Entry{Type: t.Type, Query: t.Query, LastTouched: now}
	if existed {
		e.Subscribers = prev.Subscribers
		e.LastNotifiedVersion = prev.LastNotifiedVersion
	}
	r.data[key] = e
	err := r.flushLocked()
	r.mu.Unlock()
	return !existed, err
}

// Subscribe добавляет chatID в подписчиков target'а. Создаёт запись,
// если её ещё нет (с LastTouched=now). Идемпотентно — повторная подписка
// того же chatID не плодит дубликатов. Возвращает true, если подписка
// действительно была добавлена (а не уже существовала).
func (r *Registry) Subscribe(t model.Target, chatID int64, now time.Time) (bool, error) {
	key := t.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[key]
	if !ok {
		e = Entry{Type: t.Type, Query: t.Query, LastTouched: now}
	}
	for _, id := range e.Subscribers {
		if id == chatID {
			r.data[key] = e
			return false, r.flushLocked()
		}
	}
	e.Subscribers = append(e.Subscribers, chatID)
	r.data[key] = e
	return true, r.flushLocked()
}

// Unsubscribe убирает chatID из подписчиков. Если после этого никого
// не осталось — запись остаётся (Touch её всё равно держит для кэша).
// Возвращает true, если что-то реально было убрано.
func (r *Registry) Unsubscribe(t model.Target, chatID int64) (bool, error) {
	key := t.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[key]
	if !ok {
		return false, nil
	}
	newSubs := e.Subscribers[:0:0]
	removed := false
	for _, id := range e.Subscribers {
		if id == chatID {
			removed = true
			continue
		}
		newSubs = append(newSubs, id)
	}
	if !removed {
		return false, nil
	}
	e.Subscribers = newSubs
	r.data[key] = e
	return true, r.flushLocked()
}

// SubscribersOf возвращает копию списка подписчиков target'а.
func (r *Registry) SubscribersOf(t model.Target) []int64 {
	key := t.Key()
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.data[key]
	if !ok || len(e.Subscribers) == 0 {
		return nil
	}
	out := make([]int64, len(e.Subscribers))
	copy(out, e.Subscribers)
	return out
}

// TargetsForChat возвращает все target'ы, на которые подписан chatID.
// Используется для команды /list бота.
func (r *Registry) TargetsForChat(chatID int64) []model.Target {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []model.Target
	for _, e := range r.data {
		for _, id := range e.Subscribers {
			if id == chatID {
				out = append(out, e.Target())
				break
			}
		}
	}
	return out
}

// Entry возвращает копию записи реестра по target'у. ok=false — нет такой.
func (r *Registry) Entry(t model.Target) (Entry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.data[t.Key()]
	return e, ok
}

// MarkNotified фиксирует, что для target'а уже отправлена рассылка по
// версии versionID. Дальнейшие рассылки для той же версии должны
// пропускаться.
func (r *Registry) MarkNotified(t model.Target, versionID string) error {
	key := t.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.data[key]
	if !ok {
		return nil
	}
	e.LastNotifiedVersion = versionID
	r.data[key] = e
	return r.flushLocked()
}

// Remove убирает target из реестра. Безопасно вызывать для несуществующих.
func (r *Registry) Remove(t model.Target) error {
	key := t.Key()
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[key]; !ok {
		return nil
	}
	delete(r.data, key)
	return r.flushLocked()
}

// List возвращает копию записей, отсортированную по LastTouched по убыванию
// (свежие сверху).
func (r *Registry) List() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry, 0, len(r.data))
	for _, e := range r.data {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastTouched.After(out[j].LastTouched) })
	return out
}

// Prune удаляет записи старше cutoff. Записи с непустым Subscribers не
// трогаются — пока кто-то подписан, target нужно держать горячим, иначе
// уведомления перестанут приходить. Возвращает число удалённых target'ов.
func (r *Registry) Prune(cutoff time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed int
	for key, e := range r.data {
		if len(e.Subscribers) > 0 {
			continue
		}
		if e.LastTouched.Before(cutoff) {
			delete(r.data, key)
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, r.flushLocked()
}

// flushLocked атомарно пишет реестр на диск. Должен вызываться под mu.Lock.
// Атомарность: пишем в .tmp, затем os.Rename — на posix это атомарная замена.
func (r *Registry) flushLocked() error {
	shape := fileShape{Entries: make([]Entry, 0, len(r.data))}
	for _, e := range r.data {
		shape.Entries = append(shape.Entries, e)
	}
	sort.Slice(shape.Entries, func(i, j int) bool {
		return shape.Entries[i].LastTouched.After(shape.Entries[j].LastTouched)
	})
	b, err := json.MarshalIndent(shape, "", "  ")
	if err != nil {
		return fmt.Errorf("watch: marshal: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("watch: write tmp: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		return fmt.Errorf("watch: rename: %w", err)
	}
	return nil
}
