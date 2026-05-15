package bot

import (
	"sync"
	"time"
)

// Throttle — простой per-chat rate limiter. Используется, чтобы один
// пользователь не мог завалить бота частыми командами.
type Throttle struct {
	mu       sync.Mutex
	interval time.Duration
	last     map[int64]time.Time
}

// NewThrottle создаёт throttle с минимальным интервалом между разрешениями.
func NewThrottle(interval time.Duration) *Throttle {
	return &Throttle{interval: interval, last: make(map[int64]time.Time)}
}

// Allow возвращает true, если с предыдущего разрешения для chatID прошло
// >= interval. Иначе — false (команду стоит проигнорировать).
func (t *Throttle) Allow(chatID int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if last, ok := t.last[chatID]; ok && now.Sub(last) < t.interval {
		return false
	}
	t.last[chatID] = now
	return true
}
