package schedule

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
)

var testTarget = model.Target{Type: model.TypeStudent, Query: "ИКС-531"}

// stubFetcher — управляемая реализация Fetcher для тестов.
type stubFetcher struct {
	calls atomic.Int32
	err   error
	delay time.Duration
	build func() model.Schedule // если задан — формирует Schedule, иначе минимальный шаблон
}

func (s *stubFetcher) Fetch(ctx context.Context, t model.Target) (model.Schedule, error) {
	s.calls.Add(1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return model.Schedule{}, s.err
	}
	if s.build != nil {
		return s.build(), nil
	}
	return model.Schedule{
		Target:    t,
		Title:     t.Query,
		FetchedAt: time.Now(),
		Weeks: []model.Week{
			{Name: "числитель", Days: []model.Day{{Weekday: "Понедельник", Lessons: []model.Lesson{
				{Number: 1, TimeFrom: "08:00", TimeTo: "09:35", Subject: "Физика"},
			}}}},
		},
	}, nil
}

func newService(t *testing.T, f Fetcher) (*Service, *store.Store) {
	t.Helper()
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(f, st), st
}

func TestGetCacheHit(t *testing.T) {
	stub := &stubFetcher{}
	svc, st := newService(t, stub)

	// предзаполним хранилище свежей версией
	pre := model.Schedule{Target: testTarget, FetchedAt: time.Now(), Title: "preloaded"}
	if _, _, err := st.Save(testTarget.Key(), pre); err != nil {
		t.Fatal(err)
	}

	res, err := svc.Get(context.Background(), testTarget, 1*time.Hour)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Source != SourceCache {
		t.Errorf("Source = %d, want SourceCache", res.Source)
	}
	if stub.calls.Load() != 0 {
		t.Errorf("при свежем кэше Fetcher не должен вызываться, calls=%d", stub.calls.Load())
	}
}

func TestGetCacheMiss(t *testing.T) {
	stub := &stubFetcher{}
	svc, st := newService(t, stub)

	res, err := svc.Get(context.Background(), testTarget, 1*time.Hour)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Source != SourceFresh {
		t.Errorf("Source = %d, want SourceFresh", res.Source)
	}
	if !res.Saved {
		t.Error("первый успешный fetch должен сохранить версию (Saved=true)")
	}
	if stub.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", stub.calls.Load())
	}
	if infos, _ := st.List(testTarget.Key()); len(infos) != 1 {
		t.Errorf("в истории должно быть 1 версия, %d", len(infos))
	}
}

func TestGetForceFetch(t *testing.T) {
	stub := &stubFetcher{}
	svc, st := newService(t, stub)

	// есть свежая версия в кэше
	pre := model.Schedule{Target: testTarget, FetchedAt: time.Now()}
	st.Save(testTarget.Key(), pre)

	// но maxAge=0 → принудительный фетч
	res, err := svc.Get(context.Background(), testTarget, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Source != SourceFresh {
		t.Errorf("Source = %d, want SourceFresh", res.Source)
	}
}

func TestGetStaleCacheTriggersFetch(t *testing.T) {
	stub := &stubFetcher{}
	svc, st := newService(t, stub)

	// версия в кэше старая
	old := model.Schedule{Target: testTarget, FetchedAt: time.Now().Add(-2 * time.Hour)}
	st.Save(testTarget.Key(), old)

	res, err := svc.Get(context.Background(), testTarget, 1*time.Hour)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if res.Source != SourceFresh {
		t.Errorf("устаревший кэш должен триггерить fetch")
	}
}

func TestGetUserErrorNotRecordedInMeta(t *testing.T) {
	stub := &stubFetcher{err: fmt.Errorf("wrap: %w", resolve.ErrNotFound)}
	svc, st := newService(t, stub)

	_, err := svc.Get(context.Background(), testTarget, 0)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}

	m, _ := st.ReadMeta(testTarget.Key())
	if m.LastError != "" {
		t.Errorf("ErrNotFound не должна попадать в meta.LastError, получено %q", m.LastError)
	}
}

func TestGetNetworkErrorRecorded(t *testing.T) {
	stub := &stubFetcher{err: fmt.Errorf("сайт лёг")}
	svc, st := newService(t, stub)

	_, err := svc.Get(context.Background(), testTarget, 0)
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}

	m, _ := st.ReadMeta(testTarget.Key())
	if m.LastError == "" {
		t.Error("сетевая ошибка должна попасть в meta.LastError")
	}
}

func TestSingleflightDedup(t *testing.T) {
	stub := &stubFetcher{delay: 80 * time.Millisecond}
	svc, _ := newService(t, stub)

	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _ = svc.Get(context.Background(), testTarget, 0)
		}()
	}
	wg.Wait()

	if got := stub.calls.Load(); got != 1 {
		t.Errorf("singleflight должен слить %d параллельных запросов в 1 fetch, фактически %d", N, got)
	}
}
