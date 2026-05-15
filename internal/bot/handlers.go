package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
)

const helpText = `<b>sibsutis-schedule-bot</b> — расписание SibSUTI в Telegram.

<b>Команды:</b>
/schedule — расписание по умолчанию (если задано в конфиге)
/group &lt;название&gt; — расписание группы
/teacher &lt;ФИО&gt; — расписание преподавателя
/room &lt;номер&gt; — расписание аудитории
/today, /tomorrow — на сегодня/завтра (если задан cycle_anchor)
/week numerator|denominator — конкретная неделя

<b>Подписки на изменения:</b>
/subscribe &lt;group|teacher|room&gt; &lt;запрос&gt; — подписаться
/subscriptions — мои подписки
/unsubscribe &lt;group|teacher|room&gt; &lt;запрос&gt; — отписаться

<i>При неоднозначном запросе бот покажет список вариантов — уточни запрос.</i>`

// handleMessage диспетчер команд.
func (b *Bot) handleMessage(ctx context.Context, m *tgbotapi.Message) {
	cmd, args := splitCommand(m.Text)
	switch cmd {
	case "/start", "/help", "":
		b.sendHTML(m.Chat.ID, helpText)

	case "/schedule":
		if b.cfg.DefaultTarget == nil {
			b.reply(m.Chat.ID, "В конфиге нет target'а по умолчанию. Используй /group /teacher /room.")
			return
		}
		b.showSchedule(ctx, m, *b.cfg.DefaultTarget, -1, -1)

	case "/group":
		b.parseAndShow(ctx, m, model.TypeStudent, args)
	case "/teacher":
		b.parseAndShow(ctx, m, model.TypeTeacher, args)
	case "/room":
		b.parseAndShow(ctx, m, model.TypeRoom, args)

	case "/today":
		b.handleTodayTomorrow(ctx, m, 0)
	case "/tomorrow":
		b.handleTodayTomorrow(ctx, m, 1)

	case "/week":
		b.handleWeek(ctx, m, args)

	case "/subscribe":
		b.handleSubscribe(ctx, m, args)
	case "/subscriptions":
		b.handleSubscriptions(m)
	case "/unsubscribe":
		b.handleUnsubscribe(m, args)

	default:
		if !strings.HasPrefix(cmd, "/") {
			// Не команда — показываем подсказку.
			b.sendHTML(m.Chat.ID, helpText)
			return
		}
		b.reply(m.Chat.ID, "Не знаю команду "+cmd+". /help — список.")
	}
}

// parseAndShow строит target из аргументов и показывает расписание (обе недели).
func (b *Bot) parseAndShow(ctx context.Context, m *tgbotapi.Message, typ model.TargetType, args string) {
	args = strings.TrimSpace(args)
	if args == "" {
		b.reply(m.Chat.ID, "Укажи запрос. Пример: /group ИКС-531")
		return
	}
	b.showSchedule(ctx, m, model.Target{Type: typ, Query: args}, -1, -1)
}

// showSchedule — основная точка выгрузки и отправки. weekIdx<0 — обе недели,
// dayIdx<0 — целая неделя; иначе только один день.
func (b *Bot) showSchedule(ctx context.Context, m *tgbotapi.Message, target model.Target, weekIdx, dayIdx int) {
	result, err := b.svc.Get(ctx, target, b.cfg.TelegramFreshness)
	if err != nil {
		b.handleScheduleError(m, err)
		return
	}
	s := result.Schedule

	if weekIdx >= 0 && dayIdx >= 0 {
		b.sendHTML(m.Chat.ID, FormatDay(s, weekIdx, dayIdx))
		return
	}
	if weekIdx >= 0 {
		for _, msg := range FormatWeek(s, weekIdx) {
			b.sendHTML(m.Chat.ID, msg)
		}
		return
	}
	// Обе недели.
	for w := range s.Weeks {
		for _, msg := range FormatWeek(s, w) {
			b.sendHTML(m.Chat.ID, msg)
		}
	}
}

// handleScheduleError отправляет понятную ошибку пользователю.
func (b *Bot) handleScheduleError(m *tgbotapi.Message, err error) {
	switch {
	case errors.Is(err, resolve.ErrNotFound):
		b.sendHTML(m.Chat.ID, "🤷 <i>"+html.EscapeString(err.Error())+"</i>")
	case errors.Is(err, resolve.ErrAmbiguous):
		b.sendHTML(m.Chat.ID, "❓ "+html.EscapeString(err.Error()))
	default:
		b.sendHTML(m.Chat.ID, "⚠️ <i>сайт недоступен, попробуй позже</i>\n<code>"+html.EscapeString(err.Error())+"</code>")
	}
}

// handleTodayTomorrow вычисляет текущий day-of-week / week-of-cycle по
// cycle_anchor и присылает один день. Без anchor — отвечает понятно.
func (b *Bot) handleTodayTomorrow(ctx context.Context, m *tgbotapi.Message, offsetDays int) {
	if b.cfg.DefaultTarget == nil {
		b.reply(m.Chat.ID, "Сначала задай target по умолчанию или используй /group /teacher /room.")
		return
	}
	if b.cfg.CycleAnchor.IsZero() {
		b.reply(m.Chat.ID, "Чтобы знать, какая сейчас неделя цикла, задай cycle_anchor=YYYY-MM-DD "+
			"в config.txt (понедельник любой недели числителя). "+
			"Пока показываю обе недели через /week numerator или /week denominator.")
		return
	}
	now := time.Now().Local().AddDate(0, 0, offsetDays)
	wkIdx, dayIdx := cycleIndex(b.cfg.CycleAnchor, now)
	b.showSchedule(ctx, m, *b.cfg.DefaultTarget, wkIdx, dayIdx)
}

// cycleIndex возвращает индексы недели (0=числитель, 1=знаменатель) и дня (0=Пн..6=Вс)
// для даты now относительно anchor (понедельник недели-числителя).
func cycleIndex(anchor, now time.Time) (weekIdx, dayIdx int) {
	// Приводим к началу дня в локальной TZ.
	a := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, now.Location())
	n := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	days := int(n.Sub(a).Hours() / 24)
	weeksSince := days / 7
	weekIdx = ((weeksSince % 2) + 2) % 2 // безопасное mod 2 для отрицательных

	// time.Weekday(): Sun=0..Sat=6. Нам нужно Mon=0..Sun=6.
	wd := int(now.Weekday())
	if wd == 0 {
		dayIdx = 6
	} else {
		dayIdx = wd - 1
	}
	return
}

// handleWeek показывает конкретную неделю по аргументу numerator/denominator.
func (b *Bot) handleWeek(ctx context.Context, m *tgbotapi.Message, args string) {
	if b.cfg.DefaultTarget == nil {
		b.reply(m.Chat.ID, "Сначала задай target по умолчанию или используй /group /teacher /room.")
		return
	}
	w := strings.ToLower(strings.TrimSpace(args))
	switch w {
	case "numerator", "числитель", "1":
		b.showSchedule(ctx, m, *b.cfg.DefaultTarget, 0, -1)
	case "denominator", "знаменатель", "2":
		b.showSchedule(ctx, m, *b.cfg.DefaultTarget, 1, -1)
	default:
		b.reply(m.Chat.ID, "Используй: /week numerator или /week denominator")
	}
}

// parseSubArgs разбирает "group ИКС-531" → Target.
func parseSubArgs(args string) (model.Target, error) {
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		return model.Target{}, fmt.Errorf("формат: <group|teacher|room> <запрос>")
	}
	q := strings.TrimSpace(parts[1])
	if q == "" {
		return model.Target{}, fmt.Errorf("пустой запрос")
	}
	switch strings.ToLower(parts[0]) {
	case "group":
		return model.Target{Type: model.TypeStudent, Query: q}, nil
	case "teacher":
		return model.Target{Type: model.TypeTeacher, Query: q}, nil
	case "room":
		return model.Target{Type: model.TypeRoom, Query: q}, nil
	default:
		return model.Target{}, fmt.Errorf("тип должен быть group/teacher/room, получено %q", parts[0])
	}
}

// handleSubscribe — подписка. Сначала проверяем, что target вообще резолвится.
func (b *Bot) handleSubscribe(ctx context.Context, m *tgbotapi.Message, args string) {
	target, err := parseSubArgs(args)
	if err != nil {
		b.reply(m.Chat.ID, err.Error())
		return
	}
	// Проверяем существование target'а через резолв (заодно прогреем кэш).
	if _, err := b.svc.Get(ctx, target, b.cfg.TelegramFreshness); err != nil {
		b.handleScheduleError(m, err)
		return
	}
	added, err := b.subs.Add(m.Chat.ID, target)
	if err != nil {
		b.reply(m.Chat.ID, "Не удалось сохранить подписку: "+err.Error())
		return
	}
	if added {
		b.sendHTML(m.Chat.ID, "✅ Подписка добавлена: <b>"+html.EscapeString(target.Label())+"</b>")
	} else {
		b.reply(m.Chat.ID, "Уже подписан на "+target.Label())
	}
}

// handleSubscriptions перечисляет подписки чата.
func (b *Bot) handleSubscriptions(m *tgbotapi.Message) {
	list := b.subs.List(m.Chat.ID)
	if len(list) == 0 {
		b.reply(m.Chat.ID, "У тебя нет подписок. /subscribe group ИКС-531 — добавить.")
		return
	}
	var sb strings.Builder
	sb.WriteString("<b>Твои подписки:</b>\n")
	for _, t := range list {
		sb.WriteString("• ")
		sb.WriteString(html.EscapeString(t.Label()))
		sb.WriteString("\n")
	}
	b.sendHTML(m.Chat.ID, sb.String())
}

// handleUnsubscribe — отписка.
func (b *Bot) handleUnsubscribe(m *tgbotapi.Message, args string) {
	target, err := parseSubArgs(args)
	if err != nil {
		b.reply(m.Chat.ID, err.Error())
		return
	}
	removed, err := b.subs.Remove(m.Chat.ID, target)
	if err != nil {
		b.reply(m.Chat.ID, "Не удалось обновить подписки: "+err.Error())
		return
	}
	if removed {
		b.reply(m.Chat.ID, "❎ Подписка убрана: "+target.Label())
	} else {
		b.reply(m.Chat.ID, "Такой подписки у тебя не было: "+target.Label())
	}
}
