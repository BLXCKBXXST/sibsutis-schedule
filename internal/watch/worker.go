package watch

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/diff"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
)

// Notifier — отправляет одно сообщение в чат. Совпадает по сигнатуре с
// notify.Sender — но описан здесь, чтобы пакет watch не зависел от
// notify (notify сам зависит от watch через Subscriber-интерфейс).
type Notifier interface {
	Send(ctx context.Context, chatID int64, text string) error
}

// Worker — фоновый цикл, который периодически принудительно обновляет
// все target'ы из Registry. Использует maxAge=0 → schedule.Service всегда
// идёт на my.sibsutis.ru (с дедупликацией истории).
//
// Между target'ами в одном тике вставлена случайная задержка 1-5 сек,
// чтобы не отправить N логинов одновременно при больших реестрах.
//
// Раз в сутки запускается отдельный «уборщик» — удаляет target'ы старше
// TTL (записи с подписками не трогаются).
//
// Если задан Notifier и Store — после успешного фетча воркер считает
// diff с предыдущей версией; при непустом diff и наличии подписчиков
// шлёт текст в Telegram и фиксирует LastNotifiedVersion.
type Worker struct {
	reg      *Registry
	svc      *schedule.Service
	store    *store.Store // опционально; нужен для расчёта diff
	notifier Notifier     // опционально; nil — рассылка отключена
	interval time.Duration
	ttl      time.Duration
}

// NewWorker создаёт воркер. Сам он ничего не запускает — старт через Run.
func NewWorker(reg *Registry, svc *schedule.Service, interval, ttl time.Duration) *Worker {
	return &Worker{reg: reg, svc: svc, interval: interval, ttl: ttl}
}

// WithNotifier подключает рассылку diff'а. store нужен для загрузки
// предыдущей версии. Без этого вызова рассылка отключена.
func (w *Worker) WithNotifier(st *store.Store, n Notifier) *Worker {
	w.store = st
	w.notifier = n
	return w
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
// Если фетч прошёл успешно — обновляет LastTouched. Если задан Notifier и
// у target'а есть подписчики — считает diff и рассылает изменения.
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

		// Снимок «до»: нужен, чтобы посчитать diff после Save.
		var prev model.Schedule
		var hadPrev bool
		if w.store != nil {
			if p, _, err := w.store.Latest(t.Key()); err == nil {
				prev = p
				hadPrev = true
			}
		}

		result, err := w.svc.Get(ctx, t, 0)
		if err != nil {
			log.Printf("watch: %s: %v", t.Key(), err)
			continue
		}
		_, _ = w.reg.Touch(t, time.Now())

		if result.Saved && hadPrev {
			w.maybeNotify(ctx, t, prev, result.Schedule)
		}

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

// maybeNotify считает diff между prev и next и шлёт текст всем подписчикам,
// если diff непустой и эту версию ещё не уведомляли. Идемпотентность —
// по LastNotifiedVersion в Entry.
func (w *Worker) maybeNotify(ctx context.Context, t model.Target, prev, next model.Schedule) {
	if w.notifier == nil {
		return
	}
	subs := w.reg.SubscribersOf(t)
	if len(subs) == 0 {
		return
	}
	changes := diff.DiffSchedule(prev, next)
	if len(changes) == 0 {
		return
	}
	versionID := next.FetchedAt.Format("2006-01-02T15-04-05")
	if e, ok := w.reg.Entry(t); ok && e.LastNotifiedVersion == versionID {
		return
	}
	msg := formatChanges(t, changes)
	for _, chatID := range subs {
		if err := w.notifier.Send(ctx, chatID, msg); err != nil {
			log.Printf("watch: notify %d: %v", chatID, err)
		}
	}
	if err := w.reg.MarkNotified(t, versionID); err != nil {
		log.Printf("watch: mark notified %s: %v", t.Key(), err)
	}
}

// formatChanges собирает короткое уведомление для Telegram. Без markdown —
// чтобы не возиться с экранированием. Группируем по типу изменения, чтобы
// читалось как пункты «отменили / добавили / перенесли».
func formatChanges(t model.Target, changes []diff.Change) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Расписание «%s» изменилось:\n\n", t.Label())
	for _, c := range changes {
		when := fmt.Sprintf("%s, %s", c.WeekName, c.Weekday)
		switch c.Kind {
		case diff.KindAdded:
			fmt.Fprintf(&sb, "+ %s: %s в %s, ауд. %s\n", when, c.New.Subject, c.New.TimeFrom, c.New.Room)
		case diff.KindRemoved:
			fmt.Fprintf(&sb, "− %s: %s в %s — отменена\n", when, c.Old.Subject, c.Old.TimeFrom)
		case diff.KindRoom:
			fmt.Fprintf(&sb, "→ %s: %s в %s — аудитория %s → %s\n",
				when, c.Old.Subject, c.Old.TimeFrom, c.Old.Room, c.New.Room)
		case diff.KindTime:
			fmt.Fprintf(&sb, "→ %s: %s — время %s-%s → %s-%s\n",
				when, c.Old.Subject, c.Old.TimeFrom, c.Old.TimeTo, c.New.TimeFrom, c.New.TimeTo)
		case diff.KindTeacher:
			fmt.Fprintf(&sb, "→ %s: %s в %s — преподаватель сменился\n",
				when, c.Old.Subject, c.Old.TimeFrom)
		case diff.KindType:
			fmt.Fprintf(&sb, "→ %s: %s в %s — тип занятия сменился\n",
				when, c.Old.Subject, c.Old.TimeFrom)
		}
	}
	return sb.String()
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
