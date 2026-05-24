// Package diff сравнивает две версии расписания и возвращает список
// семантических изменений: «добавили пару», «отменили», «перенесли в другую
// аудиторию», «сменили преподавателя», «сменили время», «сменили тип занятия».
//
// Используется страницей /history/.../{id1}..{id2} в веб-фронте и фоновой
// рассылкой Telegram-уведомлений в Фазе 8 — обе ожидают один и тот же
// упорядоченный список изменений.
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Kind — тип изменения.
type Kind string

const (
	KindAdded   Kind = "added"   // в новой версии появилась пара, которой не было
	KindRemoved Kind = "removed" // в новой версии нет пары, которая была
	KindTime    Kind = "time"    // изменилось время начала/конца
	KindRoom    Kind = "room"    // изменилась аудитория
	KindTeacher Kind = "teacher" // изменился преподаватель (включая количество)
	KindType    Kind = "type"    // изменился тип занятия (лекция/практика/...)
)

// Change — одно изменение между двумя версиями. Old заполнен для Removed/Time/
// Room/Teacher/Type, New — для Added/Time/Room/Teacher/Type.
type Change struct {
	Kind     Kind
	WeekIdx  int    // 0/1
	DayIdx   int    // 0..6
	WeekName string // «числитель» / «знаменатель» — для UI
	Weekday  string // «Понедельник» — для UI
	Old      model.Lesson
	New      model.Lesson
}

// DiffSchedule возвращает изменения, нужные чтобы перейти от oldS к newS.
// Пары матчатся по тройке (Number, Subject, Subgroup) — пара с тем же
// слотом и дисциплиной считается «той же», даже если у неё сменился препод
// или аудитория. Пары, отсутствующие в одной из версий целиком, попадают
// в Added/Removed.
func DiffSchedule(oldS, newS model.Schedule) []Change {
	var out []Change
	weeks := maxLen(len(oldS.Weeks), len(newS.Weeks))
	for wi := 0; wi < weeks; wi++ {
		var ow, nw model.Week
		if wi < len(oldS.Weeks) {
			ow = oldS.Weeks[wi]
		}
		if wi < len(newS.Weeks) {
			nw = newS.Weeks[wi]
		}
		days := maxLen(len(ow.Days), len(nw.Days))
		for di := 0; di < days; di++ {
			var od, nd model.Day
			if di < len(ow.Days) {
				od = ow.Days[di]
			}
			if di < len(nw.Days) {
				nd = nw.Days[di]
			}
			weekName := preferNonEmpty(nw.Name, ow.Name)
			weekday := preferNonEmpty(nd.Weekday, od.Weekday)
			out = append(out, diffDay(wi, di, weekName, weekday, od.Lessons, nd.Lessons)...)
		}
	}
	return out
}

// diffDay сравнивает пары одного дня (одного слота «числитель/знаменатель ×
// день недели») и возвращает изменения для него.
func diffDay(wi, di int, weekName, weekday string, oldL, newL []model.Lesson) []Change {
	oldByKey := indexByKey(oldL)
	newByKey := indexByKey(newL)
	var out []Change

	// Новые / изменённые.
	for _, n := range newL {
		key := lessonKey(n)
		o, ok := oldByKey[key]
		if !ok {
			out = append(out, mkChange(KindAdded, wi, di, weekName, weekday, model.Lesson{}, n))
			continue
		}
		if o.TimeFrom != n.TimeFrom || o.TimeTo != n.TimeTo {
			out = append(out, mkChange(KindTime, wi, di, weekName, weekday, o, n))
		}
		if strings.TrimSpace(o.Room) != strings.TrimSpace(n.Room) {
			out = append(out, mkChange(KindRoom, wi, di, weekName, weekday, o, n))
		}
		if !equalStringSets(o.Teachers, n.Teachers) {
			out = append(out, mkChange(KindTeacher, wi, di, weekName, weekday, o, n))
		}
		if strings.TrimSpace(o.Type) != strings.TrimSpace(n.Type) {
			out = append(out, mkChange(KindType, wi, di, weekName, weekday, o, n))
		}
	}

	// Удалённые. Стабильный порядок — как в старой версии.
	for _, o := range oldL {
		if _, kept := newByKey[lessonKey(o)]; !kept {
			out = append(out, mkChange(KindRemoved, wi, di, weekName, weekday, o, model.Lesson{}))
		}
	}
	return out
}

func mkChange(k Kind, wi, di int, weekName, weekday string, oldL, newL model.Lesson) Change {
	return Change{Kind: k, WeekIdx: wi, DayIdx: di, WeekName: weekName, Weekday: weekday, Old: oldL, New: newL}
}

// lessonKey — стабильный ключ пары в пределах одного дня. Если в один и тот
// же слот в одну подгруппу попадают две пары с одинаковой дисциплиной — они
// получают один ключ и в diff'е сольются; это редкий и нестрашный случай.
func lessonKey(l model.Lesson) string {
	return fmt.Sprintf("%d|%s|%s",
		l.Number,
		strings.TrimSpace(l.Subject),
		strings.TrimSpace(l.Subgroup),
	)
}

func indexByKey(ls []model.Lesson) map[string]model.Lesson {
	m := make(map[string]model.Lesson, len(ls))
	for _, l := range ls {
		m[lessonKey(l)] = l
	}
	return m
}

// equalStringSets сравнивает слайсы как множества (порядок не важен).
// nil и пустой слайс эквивалентны.
func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string(nil), a...)
	bc := append([]string(nil), b...)
	for i := range ac {
		ac[i] = strings.TrimSpace(ac[i])
		bc[i] = strings.TrimSpace(bc[i])
	}
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}

func preferNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func maxLen(a, b int) int {
	if a > b {
		return a
	}
	return b
}
