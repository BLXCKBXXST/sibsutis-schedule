package web

import (
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// krskLocation — часовой пояс Красноярска (Asia/Krasnoyarsk, UTC+7).
// На системах без tzdata падать не хочется, поэтому пробуем LoadLocation,
// а при неудаче берём фиксированное смещение.
var krskLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Krasnoyarsk"); err == nil {
		return loc
	}
	return time.FixedZone("KRSK", 7*3600)
}()

// todayHint — куда в расписании указывает «сегодня», чтобы шаблон знал, какой
// день подсветить. Найдено=false → расписание не покрывает текущий момент,
// шаблон ничего не подсвечивает.
type todayHint struct {
	Found      bool
	WeekIdx    int       // 0 — числитель, 1 — знаменатель (индекс в Schedule.Weeks)
	DayIdx     int       // индекс в Week.Days, 0=Понедельник..6=Воскресенье
	Today      time.Time // дата сегодняшнего «учебного» дня (или ближайшего следующего)
	IsExactDay bool      // true — Today это реально сегодня; false — мы взяли следующий учебный день
}

// computeTodayHint решает, какой день в Schedule считать «сегодняшним».
//
//  1. Если сегодня попадает на пары — берём индекс этой недели.
//  2. Иначе берём ближайший будущий учебный день из StudyDates() и помечаем
//     IsExactDay=false — шаблон в этом случае может показать «ближайший день»,
//     а не «сегодня».
//  3. Если расписание вообще без дат (старая версия истории без PROJECT_DATES) —
//     возвращаем Found=false.
func computeTodayHint(s model.Schedule, now time.Time) todayHint {
	now = now.In(krskLocation)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)

	if idx, ok := s.WeekIndexFor(todayStart); ok {
		return todayHint{
			Found:      true,
			WeekIdx:    idx,
			DayIdx:     weekdayIndex(todayStart),
			Today:      todayStart,
			IsExactDay: true,
		}
	}

	for _, d := range s.StudyDates() {
		if d.Before(todayStart) {
			continue
		}
		if idx, ok := s.WeekIndexFor(d); ok {
			return todayHint{
				Found:      true,
				WeekIdx:    idx,
				DayIdx:     weekdayIndex(d),
				Today:      d,
				IsExactDay: false,
			}
		}
	}
	return todayHint{}
}

// weekdayIndex переводит time.Weekday в индекс Schedule.Weeks[*].Days,
// где 0 — Понедельник, 6 — Воскресенье.
func weekdayIndex(t time.Time) int {
	wd := int(t.Weekday()) // воскресенье=0, понедельник=1, ..., суббота=6
	if wd == 0 {
		return 6
	}
	return wd - 1
}

// lessonRef — позиция пары в Schedule. Сравнивается в шаблоне с индексами
// текущей итерации, чтобы выделить «идущую сейчас» и «ближайшую следующую».
type lessonRef struct {
	WeekIdx, DayIdx, LessonIdx int
}

// lessonHL — то, что хочет знать UI про «здесь и сейчас»: указатели на пары
// и точный момент начала следующей. NextAt используется для live-таймера.
type lessonHL struct {
	Now, Next *lessonRef
	NextAt    time.Time
}

// highlights определяет, какая пара идёт прямо сейчас и какая будет
// следующей по расписанию. Now=nil — сейчас никто не идёт; Next=nil —
// расписание закончилось.
func highlights(s model.Schedule, now time.Time) lessonHL {
	now = now.In(krskLocation)
	todayKey := now.Format("2006-01-02")

	type candidate struct {
		ref   lessonRef
		start time.Time
	}
	var (
		cur      *lessonRef
		bestNext *candidate
	)

	for wi, w := range s.Weeks {
		for di, d := range w.Days {
			for li, l := range d.Lessons {
				from, fromErr := parseHHMMLocal(l.TimeFrom)
				to, toErr := parseHHMMLocal(l.TimeTo)
				if fromErr {
					continue
				}
				for _, ld := range l.Dates {
					begin := time.Date(ld.Year(), ld.Month(), ld.Day(), from.h, from.m, 0, 0, krskLocation)
					// Кандидат на «сейчас идёт»: сегодня + время совпало.
					if !toErr && ld.Format("2006-01-02") == todayKey {
						end := time.Date(ld.Year(), ld.Month(), ld.Day(), to.h, to.m, 0, 0, krskLocation)
						if !now.Before(begin) && now.Before(end) && cur == nil {
							ref := lessonRef{wi, di, li}
							cur = &ref
						}
					}
					// Кандидат на «следующая»: начало строго в будущем,
					// минимальное.
					if begin.After(now) {
						if bestNext == nil || begin.Before(bestNext.start) {
							bestNext = &candidate{lessonRef{wi, di, li}, begin}
						}
					}
				}
			}
		}
	}
	hl := lessonHL{Now: cur}
	if bestNext != nil {
		hl.Next = &bestNext.ref
		hl.NextAt = bestNext.start
	}
	return hl
}

// hhmmLocal — отдельный приватный парсер времени для web-слоя (model.parseHHMM
// не экспортирован, и протаскивать его сюда лишний шум).
type hhmmLocal struct{ h, m int }

func parseHHMMLocal(s string) (hhmmLocal, bool) {
	if len(s) != 5 || s[2] != ':' {
		return hhmmLocal{}, true
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return hhmmLocal{}, true
	}
	return hhmmLocal{h, m}, false
}
