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
// Использует календарную формулу model.WeekParity (а не Lesson.Dates: они
// заполнены данными о сдаче семестрового проекта и для «сегодня» не годятся).
//
//  1. Если в активной неделе (parity) в сегодняшнем дне есть пары — Found=true,
//     IsExactDay=true.
//  2. Иначе ищем ближайший будущий рабочий день по двухнедельному циклу
//     (максимум 14 итераций — гарантированно охватим оба варианта parity).
//     IsExactDay=false.
//  3. Если в расписании вообще нет пар — Found=false.
func computeTodayHint(s model.Schedule, now time.Time) todayHint {
	now = now.In(krskLocation)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)

	if len(s.Weeks) < 2 {
		return todayHint{}
	}

	for offset := 0; offset < 14; offset++ {
		date := todayStart.AddDate(0, 0, offset)
		wi := model.WeekParity(date, time.Time{})
		di := weekdayIndex(date)
		if wi < 0 || wi >= len(s.Weeks) {
			continue
		}
		if di < 0 || di >= len(s.Weeks[wi].Days) {
			continue
		}
		if len(s.Weeks[wi].Days[di].Lessons) == 0 {
			continue
		}
		return todayHint{
			Found:      true,
			WeekIdx:    wi,
			DayIdx:     di,
			Today:      date,
			IsExactDay: offset == 0,
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

// slotRef — позиция «слота» (день + момент начала) в Schedule. Используется
// вместо ссылки на конкретный Lesson: если в одном временно́м слоте лежит
// несколько пар (типичный случай — параллельные подгруппы по разным
// предметам), все они должны подсветиться как одна «идущая сейчас» /
// «следующая». TimeFrom однозначно идентифицирует слот внутри (WeekIdx,
// DayIdx) — пары с одним TimeFrom считаем одним слотом.
type slotRef struct {
	WeekIdx, DayIdx int
	TimeFrom        string
}

// lessonHL — то, что хочет знать UI про «здесь и сейчас»: указатели на
// слоты и точный момент начала следующего. NextAt используется для
// live-таймера.
type lessonHL struct {
	Now, Next *slotRef
	NextAt    time.Time
}

// highlights определяет, какой слот идёт прямо сейчас и какой будет
// следующим по двухнедельному циклу. Now=nil — сейчас никто не идёт;
// Next=nil — расписание совсем пустое.
//
// Алгоритм: считаем parity сегодняшней недели через model.WeekParity.
// Для «сейчас идёт» проверяем только слоты из Weeks[parity].Days[weekday].
// Для «следующего» итерируем 14 дней вперёд начиная с сегодня — на каждом
// дне выбираем подходящую неделю Schedule и проверяем все пары этого дня;
// слот с минимальным begin > now выигрывает.
//
// Идентификация по (WeekIdx, DayIdx, TimeFrom) гарантирует, что несколько
// Lesson-строк одного слота (подгруппы) подсвечиваются одновременно.
func highlights(s model.Schedule, now time.Time) lessonHL {
	now = now.In(krskLocation)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, krskLocation)

	if len(s.Weeks) < 2 {
		return lessonHL{}
	}

	var (
		cur      *slotRef
		bestNext *slotRef
		bestAt   time.Time
	)

	for offset := 0; offset < 14; offset++ {
		date := todayStart.AddDate(0, 0, offset)
		wi := model.WeekParity(date, time.Time{})
		di := weekdayIndex(date)
		if wi >= len(s.Weeks) || di >= len(s.Weeks[wi].Days) {
			continue
		}
		for _, l := range s.Weeks[wi].Days[di].Lessons {
			from, ferr := parseHHMMLocal(l.TimeFrom)
			to, terr := parseHHMMLocal(l.TimeTo)
			if ferr {
				continue
			}
			begin := time.Date(date.Year(), date.Month(), date.Day(), from.h, from.m, 0, 0, krskLocation)

			// is-now: слот в сегодняшнем дне, now попадает в [begin, end).
			// Достаточно одного матча — этот слот применится ко всем парам
			// с тем же TimeFrom в (wi, di).
			if offset == 0 && !terr && cur == nil {
				end := time.Date(date.Year(), date.Month(), date.Day(), to.h, to.m, 0, 0, krskLocation)
				if !now.Before(begin) && now.Before(end) {
					ref := slotRef{wi, di, l.TimeFrom}
					cur = &ref
				}
			}

			// is-next: слот с минимальным begin > now.
			if begin.After(now) && (bestNext == nil || begin.Before(bestAt)) {
				ref := slotRef{wi, di, l.TimeFrom}
				bestNext = &ref
				bestAt = begin
			}
		}
	}

	hl := lessonHL{Now: cur}
	if bestNext != nil {
		hl.Next = bestNext
		hl.NextAt = bestAt
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
