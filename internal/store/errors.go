package store

import "errors"

// ErrNoHistory возвращается, когда в хранилище ещё нет ни одной сохранённой
// версии расписания.
var ErrNoHistory = errors.New("история пуста: расписание ещё ни разу не выгружалось")
