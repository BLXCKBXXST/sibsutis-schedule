package model

import (
	"testing"
	"time"
)

func TestWeekParity(t *testing.T) {
	loc := time.UTC

	type tc struct {
		name string
		date time.Time
		want int // 0 = числитель, 1 = знаменатель
	}
	cases := []tc{
		// 01.09.2025 — это понедельник; опорная точка → числитель.
		{"01.09.2025 (опорная, пн)", time.Date(2025, 9, 1, 0, 0, 0, 0, loc), 0},
		// 02.09.2025 (вт) — в той же неделе, тоже числитель.
		{"02.09.2025 (та же неделя)", time.Date(2025, 9, 2, 0, 0, 0, 0, loc), 0},
		// 08.09.2025 — пн следующей недели → знаменатель.
		{"08.09.2025 (+1 неделя)", time.Date(2025, 9, 8, 0, 0, 0, 0, loc), 1},
		// 15.09.2025 → числитель снова.
		{"15.09.2025 (+2 недели)", time.Date(2025, 9, 15, 0, 0, 0, 0, loc), 0},
		// Наш репер: пн 25.05.2026 — числитель (подтверждено пользователем).
		{"25.05.2026 (пн, репер)", time.Date(2026, 5, 25, 0, 0, 0, 0, loc), 0},
		// Воскресенье 24.05.2026 относится к ИСТЕКАЮЩЕЙ неделе 18.05..24.05,
		// которая раньше 25.05 → должна быть противоположной (знаменатель).
		{"24.05.2026 (вс пред. недели)", time.Date(2026, 5, 24, 0, 0, 0, 0, loc), 1},
		// Дата до 1 сентября 2025 относится к прошлому учебному году
		// (anchor = 01.09.2024 — воскресенье; mondayOf даёт пн 26.08.2024).
		{"30.08.2025 (учебный год 2024/25)", time.Date(2025, 8, 30, 0, 0, 0, 0, loc), 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WeekParity(c.date, time.Time{})
			if got != c.want {
				t.Errorf("WeekParity(%s) = %d, want %d",
					c.date.Format("2006-01-02 Mon"), got, c.want)
			}
		})
	}
}

func TestWeekParity_CustomAnchor(t *testing.T) {
	loc := time.UTC
	// Опорная точка — произвольный понедельник.
	anchor := time.Date(2026, 2, 9, 0, 0, 0, 0, loc) // пн 09.02.2026
	// От этого понедельника:
	if got := WeekParity(anchor, anchor); got != 0 {
		t.Errorf("anchor сам по себе → %d, want 0", got)
	}
	// +1 неделя → знаменатель.
	if got := WeekParity(anchor.AddDate(0, 0, 7), anchor); got != 1 {
		t.Errorf("+1 неделя → %d, want 1", got)
	}
	// +2 недели → числитель.
	if got := WeekParity(anchor.AddDate(0, 0, 14), anchor); got != 0 {
		t.Errorf("+2 недели → %d, want 0", got)
	}
	// Дата до anchor'а (на 1 неделю назад) — тоже должна корректно нормироваться.
	if got := WeekParity(anchor.AddDate(0, 0, -7), anchor); got != 1 {
		t.Errorf("-1 неделя → %d, want 1", got)
	}
}

func TestMondayOf(t *testing.T) {
	loc := time.UTC
	mon := time.Date(2026, 5, 25, 14, 30, 0, 0, loc) // пн 25.05 14:30
	if got := mondayOf(mon); !got.Equal(time.Date(2026, 5, 25, 0, 0, 0, 0, loc)) {
		t.Errorf("понедельник: %v", got)
	}
	sun := time.Date(2026, 5, 31, 23, 59, 0, 0, loc) // вс 31.05
	if got := mondayOf(sun); !got.Equal(time.Date(2026, 5, 25, 0, 0, 0, 0, loc)) {
		t.Errorf("воскресенье: %v", got)
	}
}
