// Package store хранит историю версий расписания на диске.
//
// История ведётся отдельно для каждого target'а (группа/преподаватель/аудитория):
// каждый target — свой подкаталог, внутри — JSON-файлы версий с таймстампом в
// имени и meta.json с информацией о последней проверке. Если содержимое
// расписания не изменилось по сравнению с последней версией, новый файл не
// создаётся — история растёт только при реальных изменениях.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// idLayout — формат таймстампа в имени файла версии (без двоеточий, чтобы имя
// было валидным на любой ФС).
const idLayout = "2006-01-02T15-04-05"

// metaFile — имя файла со служебной информацией внутри каталога target'а.
const metaFile = "meta.json"

// Store — история версий в каталоге данных.
type Store struct {
	dir        string // корневой каталог данных
	historyDir string // dir/history
}

// VersionInfo — краткая сводка по одной сохранённой версии (для команды history).
type VersionInfo struct {
	ID        string    `json:"id"`         // идентификатор версии = имя файла без .json
	FetchedAt time.Time `json:"fetched_at"` // когда выгружено
	Title     string    `json:"title,omitempty"`
	Lessons   int       `json:"lessons"`
}

// Meta — служебная информация о последних проверках target'а.
type Meta struct {
	LastCheck   time.Time `json:"last_check"`             // последний запуск выгрузки (успех или нет)
	LastSuccess time.Time `json:"last_success,omitempty"` // последняя успешная выгрузка
	LastError   string    `json:"last_error,omitempty"`   // текст последней ошибки
}

// TargetSummary — сводка по одному target'у в истории (для команды history без флага).
type TargetSummary struct {
	Key       string       // ключ каталога истории
	Target    model.Target // что это за target (из последней версии)
	Versions  int          // число сохранённых версий
	LatestAt  time.Time    // время последней версии
	LastCheck time.Time    // время последней проверки
	LastError string       // последняя ошибка, если была
}

// DefaultDir возвращает каталог данных по умолчанию:
// $XDG_DATA_HOME/sibsutis-schedule или ~/.local/share/sibsutis-schedule.
func DefaultDir() (string, error) {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "sibsutis-schedule"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("не удалось определить домашний каталог: %w", err)
	}
	return filepath.Join(home, ".local", "share", "sibsutis-schedule"), nil
}

// New открывает (создавая при необходимости) хранилище в каталоге dir.
// Если dir пуст — используется DefaultDir.
func New(dir string) (*Store, error) {
	if dir == "" {
		var err error
		if dir, err = DefaultDir(); err != nil {
			return nil, err
		}
	}
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		return nil, fmt.Errorf("не удалось создать каталог данных %s: %w", historyDir, err)
	}
	return &Store{dir: dir, historyDir: historyDir}, nil
}

// Dir возвращает корневой каталог данных.
func (s *Store) Dir() string { return s.dir }

// keyDir — каталог истории конкретного target'а.
func (s *Store) keyDir(key string) string { return filepath.Join(s.historyDir, key) }

// metaPath — путь к meta.json конкретного target'а.
func (s *Store) metaPath(key string) string { return filepath.Join(s.keyDir(key), metaFile) }

// Save сохраняет расписание новой версией в истории target'а key. Если
// содержимое совпадает с последней сохранённой версией, файл не создаётся:
// saved == false, id — идентификатор существующей последней версии.
func (s *Store) Save(key string, sched model.Schedule) (saved bool, id string, err error) {
	if last, lastID, lerr := s.Latest(key); lerr == nil {
		if contentHash(last) == contentHash(sched) {
			return false, lastID, nil
		}
	}

	if sched.FetchedAt.IsZero() {
		sched.FetchedAt = time.Now()
	}
	dir := s.keyDir(key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, "", fmt.Errorf("не удалось создать каталог %s: %w", dir, err)
	}

	id = sched.FetchedAt.Format(idLayout)
	path := filepath.Join(dir, id+".json")
	// На случай коллизии по секунде — добавляем суффикс.
	for i := 1; fileExists(path); i++ {
		id = fmt.Sprintf("%s-%d", sched.FetchedAt.Format(idLayout), i)
		path = filepath.Join(dir, id+".json")
	}

	data, err := json.MarshalIndent(sched, "", "  ")
	if err != nil {
		return false, "", fmt.Errorf("сериализация расписания: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, "", fmt.Errorf("запись версии %s: %w", path, err)
	}
	return true, id, nil
}

// Latest возвращает самую свежую сохранённую версию для target'а key.
func (s *Store) Latest(key string) (model.Schedule, string, error) {
	ids, err := s.ids(key)
	if err != nil {
		return model.Schedule{}, "", err
	}
	if len(ids) == 0 {
		return model.Schedule{}, "", ErrNoHistory
	}
	latest := ids[len(ids)-1]
	sched, err := s.Load(key, latest)
	return sched, latest, err
}

// Load читает конкретную версию target'а key по идентификатору.
func (s *Store) Load(key, id string) (model.Schedule, error) {
	path := filepath.Join(s.keyDir(key), id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return model.Schedule{}, fmt.Errorf("версия %q не найдена", id)
		}
		return model.Schedule{}, fmt.Errorf("чтение версии %s: %w", path, err)
	}
	var sched model.Schedule
	if err := json.Unmarshal(data, &sched); err != nil {
		return model.Schedule{}, fmt.Errorf("разбор версии %s: %w", path, err)
	}
	return sched, nil
}

// List возвращает сводку по всем версиям target'а key, от старых к новым.
func (s *Store) List(key string) ([]VersionInfo, error) {
	ids, err := s.ids(key)
	if err != nil {
		return nil, err
	}
	infos := make([]VersionInfo, 0, len(ids))
	for _, id := range ids {
		sched, err := s.Load(key, id)
		if err != nil {
			return nil, err
		}
		infos = append(infos, VersionInfo{
			ID:        id,
			FetchedAt: sched.FetchedAt,
			Title:     sched.Title,
			Lessons:   sched.LessonCount(),
		})
	}
	return infos, nil
}

// ListLatest возвращает последние n версий target'а key в порядке от новых
// к старым. n<=0 → без лимита (все версии). Удобно для страницы истории,
// где обычно нужны только последние 10–20 снапшотов.
func (s *Store) ListLatest(key string, n int) ([]VersionInfo, error) {
	ids, err := s.ids(key)
	if err != nil {
		return nil, err
	}
	// ids() уже отсортированы по возрастанию — обходим с конца.
	if n <= 0 || n > len(ids) {
		n = len(ids)
	}
	infos := make([]VersionInfo, 0, n)
	for i := len(ids) - 1; i >= 0 && len(infos) < n; i-- {
		sched, err := s.Load(key, ids[i])
		if err != nil {
			return nil, err
		}
		infos = append(infos, VersionInfo{
			ID:        ids[i],
			FetchedAt: sched.FetchedAt,
			Title:     sched.Title,
			Lessons:   sched.LessonCount(),
		})
	}
	return infos, nil
}

// Targets возвращает сводку по всем target'ам, для которых есть история.
func (s *Store) Targets() ([]TargetSummary, error) {
	entries, err := os.ReadDir(s.historyDir)
	if err != nil {
		return nil, fmt.Errorf("чтение каталога истории %s: %w", s.historyDir, err)
	}
	var summaries []TargetSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		key := e.Name()
		ids, err := s.ids(key)
		if err != nil {
			return nil, err
		}
		sum := TargetSummary{Key: key, Versions: len(ids)}
		if len(ids) > 0 {
			if sched, lerr := s.Load(key, ids[len(ids)-1]); lerr == nil {
				sum.Target = sched.Target
				sum.LatestAt = sched.FetchedAt
			}
		}
		if m, merr := s.ReadMeta(key); merr == nil {
			sum.LastCheck = m.LastCheck
			sum.LastError = m.LastError
		}
		summaries = append(summaries, sum)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Key < summaries[j].Key })
	return summaries, nil
}

// ids возвращает отсортированные идентификаторы версий target'а key.
func (s *Store) ids(key string) ([]string, error) {
	entries, err := os.ReadDir(s.keyDir(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // каталога target'а ещё нет — история пуста
		}
		return nil, fmt.Errorf("чтение каталога истории %s: %w", s.keyDir(key), err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == metaFile || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(ids) // лексикографическая сортировка == хронологическая для idLayout
	return ids, nil
}

// ReadMeta читает meta.json target'а key. Если файла нет — возвращает пустую Meta.
func (s *Store) ReadMeta(key string) (Meta, error) {
	data, err := os.ReadFile(s.metaPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return Meta{}, nil
		}
		return Meta{}, fmt.Errorf("чтение %s: %w", metaFile, err)
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return Meta{}, fmt.Errorf("разбор %s: %w", metaFile, err)
	}
	return m, nil
}

// RecordCheck фиксирует факт проверки сайта для target'а key. При успехе errMsg пуст.
func (s *Store) RecordCheck(key string, success bool, errMsg string) error {
	m, err := s.ReadMeta(key)
	if err != nil {
		return err
	}
	dir := s.keyDir(key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("не удалось создать каталог %s: %w", dir, err)
	}

	now := time.Now()
	m.LastCheck = now
	if success {
		m.LastSuccess = now
		m.LastError = ""
	} else {
		m.LastError = errMsg
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("сериализация %s: %w", metaFile, err)
	}
	if err := os.WriteFile(s.metaPath(key), data, 0o600); err != nil {
		return fmt.Errorf("запись %s: %w", metaFile, err)
	}
	return nil
}

// contentHash считает хэш содержимого расписания без учёта момента выгрузки и
// сырого HTML — чтобы дедуп срабатывал при идентичном расписании.
func contentHash(s model.Schedule) string {
	c := s
	c.FetchedAt = time.Time{}
	c.RawHTML = ""
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
