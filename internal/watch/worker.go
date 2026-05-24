package watch

import (
	"context"
	"log"
	"math/rand/v2"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
)

// Worker — фоновый цикл, который периодически принудительно обновляет
// все target'ы из Registry. Использует maxAge=0 → schedule.Service всегда
// идёт на my.sibsutis.ru (с дедупликацией истории).
//
// Между target'ами в одном тике вставлена случайная задержка 1-5 сек,
// чтобы не отправить N логинов одновременно при больших реестрах.
//
// Раз в сутки запускается отдельный «уборщик» — удаляет target'ы старше
// TTL.
type Worker struct {
	reg      *Registry
	svc      *schedule.Service
	interval time.Duration
	ttl      time.Duration
}

// NewWorker создаёт воркер. Сам он ничего не запускает — старт через Run.
func NewWorker(reg *Registry, svc *schedule.Service, interval, ttl time.Duration) *Worker {
	return &Worker{reg: reg, svc: svc, interval: interval, ttl: ttl}
}

// Run блокирует горутину до отмены ctx. Делает один цикл обхода сразу
// после старта, дальше — по тикеру. Возврат происходит только при
// ctx.Done().
func (w *Worker) Run(ctx context.Context) {
	log.Printf("watch: воркер стартовал, interval=%s ttl=%s", w.interval, w.ttl)
	w.tick(ctx)

	t := time.NewTicker(w.interval)
	defer t.Stop()
	// GC: раз в сутки (или каждый interval, если он больше).
	gcEvery := 24 * time.Hour
	if w.interval > gcEvery {
		gcEvery = w.interval
	}
	gcTicker := time.NewTicker(gcEvery)
	defer gcTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("watch: воркер остановлен")
			return
		case <-t.C:
			w.tick(ctx)
		case <-gcTicker.C:
			cutoff := time.Now().Add(-w.ttl)
			if n, err := w.reg.Prune(cutoff); err != nil {
				log.Printf("watch: prune: %v", err)
			} else if n > 0 {
				log.Printf("watch: prune убрал %d устаревших target'ов", n)
			}
		}
	}
}

// tick — один проход реестра. Обновляет каждый target через svc.Get(maxAge=0).
// Если фетч прошёл успешно — обновляет LastTouched, чтобы запись не
// считалась «свежей» исключительно по последнему просмотру, но и по
// последнему успешному обновлению (иначе target, кэш которого живой, мог
// бы выпасть из реестра после TTL).
func (w *Worker) tick(ctx context.Context) {
	entries := w.reg.List()
	if len(entries) == 0 {
		return
	}
	log.Printf("watch: обновляю %d target'ов", len(entries))
	for _, e := range entries {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t := e.Target()
		if _, err := w.svc.Get(ctx, t, 0); err != nil {
			log.Printf("watch: %s: %v", t.Key(), err)
			continue
		}
		_, _ = w.reg.Touch(t, time.Now())

		// Случайная пауза между target'ами, чтобы не залить my.sibsutis.ru
		// одновременным шквалом логинов.
		jitter := time.Duration(1000+rand.IntN(4000)) * time.Millisecond
		select {
		case <-ctx.Done():
			return
		case <-time.After(jitter):
		}
	}
}

// AsTouchHook возвращает Hook, который веб-сервер дёргает при каждом
// просмотре /schedule/{type}/{q} — добавляет target в реестр (если ещё
// нет) или обновляет LastTouched.
type Hook func(model.Target)

func (w *Worker) TouchHook() Hook {
	return func(t model.Target) {
		if _, err := w.reg.Touch(t, time.Now()); err != nil {
			log.Printf("watch: touch %s: %v", t.Key(), err)
		}
	}
}
