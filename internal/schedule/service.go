// Package schedule — высокоуровневая «получить расписание target'а» с дедупом
// параллельных запросов и кэшированием по свежести версии в истории.
//
// Этот слой нужен открытому Telegram-боту: он защищает my.sibsutis.ru от
// частых одинаковых фетчей и сливает одновременные запросы одного target'а в
// один HTTP-цикл (singleflight). CLI тоже его использует, чтобы не было
// дублирования логики «логин → resolve → fetch → parse → save».
package schedule

import (
	"context"
	"errors"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/auth"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/fetch"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/parse"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
)

// DefaultHTTPTimeout — таймаут на сетевые операции (логин + resolve + fetch).
const DefaultHTTPTimeout = 25 * time.Second

// Source — откуда взято расписание.
type Source int

const (
	SourceCache Source = iota // последняя версия из истории
	SourceFresh               // только что выгружено с сайта
)

// Result — то, что возвращает Service.Get.
type Result struct {
	Schedule model.Schedule
	Source   Source
	Saved    bool // если был fetch — была ли сохранена новая версия (false при дедупе)
}

// Fetcher вытягивает расписание target'а с сайта. Существует ради тестов:
// в production используется HTTPFetcher, в тестах — stub.
type Fetcher interface {
	Fetch(ctx context.Context, t model.Target) (model.Schedule, error)
}

// Service координирует кэш-свежесть и singleflight поверх Fetcher и store.
type Service struct {
	fetcher Fetcher
	store   *store.Store
	group   singleflight.Group
}

// New собирает Service.
func New(fetcher Fetcher, st *store.Store) *Service {
	return &Service{fetcher: fetcher, store: st}
}

// Get возвращает расписание target'а. Если последняя версия в истории моложе
// maxAge — отдаёт её без обращения к сайту; иначе делает свежий fetch
// (параллельные запросы одного target'а сливаются в один). maxAge == 0 —
// форсированный fetch (используется фоновым циклом обновлений).
func (s *Service) Get(ctx context.Context, t model.Target, maxAge time.Duration) (Result, error) {
	key := t.Key()

	if maxAge > 0 {
		if sched, _, err := s.store.Latest(key); err == nil && !sched.FetchedAt.IsZero() {
			if time.Since(sched.FetchedAt) < maxAge {
				return Result{Schedule: sched, Source: SourceCache}, nil
			}
		}
	}

	v, err, _ := s.group.Do(key, func() (any, error) {
		sched, err := s.fetcher.Fetch(ctx, t)
		if err != nil {
			// Ошибки запроса (NotFound/Ambiguous) — не от сайта, в meta не пишем.
			if isNetworkError(err) {
				_ = s.store.RecordCheck(key, false, err.Error())
			}
			return nil, err
		}
		saved, _, _ := s.store.Save(key, sched)
		_ = s.store.RecordCheck(key, true, "")
		return fetchOutcome{sched: sched, saved: saved}, nil
	})
	if err != nil {
		return Result{}, err
	}
	o := v.(fetchOutcome)
	return Result{Schedule: o.sched, Source: SourceFresh, Saved: o.saved}, nil
}

type fetchOutcome struct {
	sched model.Schedule
	saved bool
}

// isNetworkError отличает «сайт лёг / не отвечает» от «запрос пользователя
// некорректен» (NotFound / Ambiguous). Только сетевые ошибки попадают в meta.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, resolve.ErrNotFound) || errors.Is(err, resolve.ErrAmbiguous) {
		return false
	}
	return true
}

// HTTPFetcher — production реализация Fetcher: ходит на сайт через
// auth → resolve → fetch → parse.
type HTTPFetcher struct {
	Cfg     *config.Config
	Timeout time.Duration
}

// Fetch выполняет полный онлайн-цикл выгрузки.
func (h *HTTPFetcher) Fetch(ctx context.Context, t model.Target) (model.Schedule, error) {
	_ = ctx // тайм-аут пока задаётся через http.Client, ctx прокинем при необходимости
	timeout := h.Timeout
	if timeout == 0 {
		timeout = DefaultHTTPTimeout
	}
	client, err := auth.Login(h.Cfg.AuthURL, h.Cfg.Login, h.Cfg.Password, timeout)
	if err != nil {
		return model.Schedule{}, err
	}
	match, err := resolve.Resolve(client, h.Cfg.ScheduleURL, t)
	if err != nil {
		return model.Schedule{}, err
	}
	html, err := fetch.Schedule(client, h.Cfg.ScheduleURL, t, match.ID)
	if err != nil {
		return model.Schedule{}, err
	}
	sched, err := parse.ParseSchedule(html)
	if err != nil {
		return model.Schedule{}, err
	}
	sched.FetchedAt = time.Now()
	sched.Target = t
	sched.Title = match.Text
	return sched, nil
}
