package model

import (
	"strings"
	"unicode"
)

// TargetType — тип расписания на сайте: по группе, по преподавателю или по аудитории.
type TargetType string

const (
	TypeStudent TargetType = "student"
	TypeTeacher TargetType = "teacher"
	TypeRoom    TargetType = "room"
)

// Target — что именно запрашивать: тип расписания и человекочитаемый запрос
// (название группы, ФИО преподавателя или номер аудитории).
type Target struct {
	Type  TargetType `json:"type"`
	Query string     `json:"query"`
}

// TypeMeta — параметры сайта, специфичные для типа расписания.
type TypeMeta struct {
	AjaxPath  string // путь AJAX-эндпоинта поиска (get_groups_soap.php и т.п.)
	AjaxParam string // имя GET-параметра поиска (search_group и т.п.)
	URLParam  string // имя GET-параметра в URL страницы расписания (group/teacher/room)
	Label     string // подпись для вывода: "группа" / "преподаватель" / "аудитория"
}

// typeMeta описывает, как обращаться к сайту для каждого типа расписания.
var typeMeta = map[TargetType]TypeMeta{
	TypeStudent: {AjaxPath: "/ajax/get_groups_soap.php", AjaxParam: "search_group", URLParam: "group", Label: "группа"},
	TypeTeacher: {AjaxPath: "/ajax/get_pps.php", AjaxParam: "search_pps", URLParam: "teacher", Label: "преподаватель"},
	TypeRoom:    {AjaxPath: "/ajax/get_room.php", AjaxParam: "search_room", URLParam: "room", Label: "аудитория"},
}

// Meta возвращает параметры сайта для типа target'а и признак того, что тип известен.
func (t Target) Meta() (TypeMeta, bool) {
	m, ok := typeMeta[t.Type]
	return m, ok
}

// Valid сообщает, что тип target'а известен и запрос не пуст.
func (t Target) Valid() bool {
	_, ok := typeMeta[t.Type]
	return ok && strings.TrimSpace(t.Query) != ""
}

// Key возвращает безопасный для файловой системы ключ target'а — имя каталога
// истории. Например: "student-иб-211", "teacher-иванов-и-и", "room-1-101".
func (t Target) Key() string {
	return string(t.Type) + "-" + slug(t.Query)
}

// Label — человекочитаемое описание target'а, напр. "группа ИБ-211".
func (t Target) Label() string {
	if m, ok := typeMeta[t.Type]; ok {
		return m.Label + " " + strings.TrimSpace(t.Query)
	}
	return string(t.Type) + " " + strings.TrimSpace(t.Query)
}

// slug приводит произвольную строку к нижнему регистру, заменяя любые
// не буквенно-цифровые символы на дефис и схлопывая повторы.
func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
