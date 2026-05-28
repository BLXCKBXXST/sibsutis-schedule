package model

import "time"

// WeekParity возвращает индекс активной недели для указанной даты в
// двухнедельном цикле «числитель/знаменатель»: 0 — числитель, 1 — знаменатель.
//
// Алгоритм воспроизводит JS-логику со страницы расписания my.sibsutis.ru:
// опорной точкой считается понедельник той учебной недели, в которой
// находится 1 сентября соответствующего учебного года. Все последующие
// понедельники с чётным сдвигом от опорной — числитель, нечётным — знаменатель.
//
// anchor — опциональная переопределённая опорная дата (например, из config).
// Если anchor.IsZero() — берётся вычисленная по 1 сентября. Локация anchor'а
// для расчёта неважна, используется только календарная дата.
//
// Verified: пн 25.05.2026 → 0 (числитель). От пн 01.09.2025 (день недели = пн)
// до пн 25.05.2026 проходит ровно 38 недель.
func WeekParity(date time.Time, anchor time.Time) int {
	if anchor.IsZero() {
		anchor = defaultAnchor(date)
	}
	anchorMon := mondayOf(anchor)
	dateMon := mondayOf(date)

	days := int(dateMon.Sub(anchorMon).Hours() / 24)
	weeks := days / 7
	// На отрицательных weeks % 2 может вернуть -1 — нормализуем к 0/1.
	parity := weeks % 2
	if parity < 0 {
		parity += 2
	}
	return parity
}

// defaultAnchor — 1 сентября учебного года, к которому относится date.
// Если date >= 1 сентября текущего календарного года — берём 1 сентября
// этого года; иначе — прошлого.
func defaultAnchor(date time.Time) time.Time {
	loc := date.Location()
	sep1 := time.Date(date.Year(), time.September, 1, 0, 0, 0, 0, loc)
	if date.Before(sep1) {
		sep1 = time.Date(date.Year()-1, time.September, 1, 0, 0, 0, 0, loc)
	}
	return sep1
}

// mondayOf возвращает понедельник той же ISO-недели, что и t (00:00 в локации t).
// Пн → t, Вс → t-6 дней. Используется и из web-слоя.
func mondayOf(t time.Time) time.Time {
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7 // Sunday → 7
	}
	shift := wd - 1 // Mon=0, Tue=1, ..., Sun=6
	return time.Date(t.Year(), t.Month(), t.Day()-shift, 0, 0, 0, 0, t.Location())
}

// NextDateFor возвращает ближайшую дату (день, без времени) ≥ from, у
// которой WeekParity == wi и weekdayIndex == di. wi: 0=числитель, 1=знаменатель;
// di: 0=Понедельник..6=Воскресенье. Используется для подстановки конкретной
// календарной даты в diff-страницу и Telegram-уведомления — чтобы пользователю
// не приходилось знать жаргон «числитель/знаменатель».
//
// Перебор гарантированно сходится за ≤14 итераций (двухнедельный цикл).
func NextDateFor(wi, di int, from time.Time) time.Time {
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	for off := 0; off < 14; off++ {
		d := day.AddDate(0, 0, off)
		if WeekParity(d, time.Time{}) == wi && weekdayOf(d) == di {
			return d
		}
	}
	return time.Time{}
}

// weekdayOf — индекс дня недели (0=Пн..6=Вс). Дублирует web.weekdayIndex,
// но нужен в model, чтобы NextDateFor не зависел от web-пакета.
func weekdayOf(t time.Time) int {
	wd := int(t.Weekday())
	if wd == 0 {
		return 6
	}
	return wd - 1
}
