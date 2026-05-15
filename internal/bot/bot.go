package bot

import (
	"context"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/subs"
)

// DefaultRefreshInterval — как часто бот в фоне обновляет расписание target'ов
// (default target + все, на которые есть подписки), чтобы поймать изменения.
const DefaultRefreshInterval = 30 * time.Minute

// DefaultThrottleInterval — минимальный интервал между командами одного чата.
const DefaultThrottleInterval = 3 * time.Second

// Bot — Telegram-бот: long-polling + фоновое обновление подписок.
type Bot struct {
	api      *tgbotapi.BotAPI
	cfg      *config.Config
	svc      *schedule.Service
	store    *store.Store
	subs     *subs.Store
	throttle *Throttle
	refresh  time.Duration
}

// New собирает бота. api должен быть уже сконструирован и аутентифицирован.
func New(api *tgbotapi.BotAPI, cfg *config.Config, svc *schedule.Service, st *store.Store, ss *subs.Store) *Bot {
	return &Bot{
		api:      api,
		cfg:      cfg,
		svc:      svc,
		store:    st,
		subs:     ss,
		throttle: NewThrottle(DefaultThrottleInterval),
		refresh:  DefaultRefreshInterval,
	}
}

// Run запускает long-polling и фоновое обновление подписок. Возвращается
// при отмене ctx.
func (b *Bot) Run(ctx context.Context) error {
	// Регистрируем меню команд в клиенте Telegram (то самое выпадающее меню при /).
	if err := b.registerCommands(); err != nil {
		log.Printf("registerCommands: %v", err)
	}

	go b.refreshLoop(ctx)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)

	log.Printf("bot @%s запущен", b.api.Self.UserName)

	for {
		select {
		case <-ctx.Done():
			b.api.StopReceivingUpdates()
			return ctx.Err()
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			go b.handleUpdate(ctx, update)
		}
	}
}

// registerCommands задаёт меню команд бота (то, что показывается в клиенте при /).
func (b *Bot) registerCommands() error {
	cmds := tgbotapi.NewSetMyCommands(
		tgbotapi.BotCommand{Command: "schedule", Description: "Расписание по умолчанию (если задано)"},
		tgbotapi.BotCommand{Command: "group", Description: "Расписание группы"},
		tgbotapi.BotCommand{Command: "teacher", Description: "Расписание преподавателя"},
		tgbotapi.BotCommand{Command: "room", Description: "Расписание аудитории"},
		tgbotapi.BotCommand{Command: "today", Description: "Расписание на сегодня"},
		tgbotapi.BotCommand{Command: "tomorrow", Description: "Расписание на завтра"},
		tgbotapi.BotCommand{Command: "week", Description: "Числитель или знаменатель"},
		tgbotapi.BotCommand{Command: "subscribe", Description: "Подписаться на изменения"},
		tgbotapi.BotCommand{Command: "subscriptions", Description: "Мои подписки"},
		tgbotapi.BotCommand{Command: "unsubscribe", Description: "Отписаться от изменений"},
		tgbotapi.BotCommand{Command: "help", Description: "Подсказка"},
	)
	_, err := b.api.Request(cmds)
	return err
}

// handleUpdate — обработка одного обновления (сообщение или callback).
func (b *Bot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic в handleUpdate: %v", r)
		}
	}()

	if update.Message != nil {
		if !b.throttle.Allow(update.Message.Chat.ID) {
			return // слишком часто — молча игнорируем
		}
		b.handleMessage(ctx, update.Message)
	}
}

// refreshLoop фоном обновляет расписания, на которые есть подписки (+ default
// target). По любому изменению — шлёт уведомления подписчикам.
func (b *Bot) refreshLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic в refreshLoop: %v", r)
		}
	}()

	t := time.NewTicker(b.refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.refreshAll(ctx)
		}
	}
}

// refreshAll форсированно обновляет default target и все target'ы из подписок.
// На saved=true → шлёт уведомления подписчикам.
func (b *Bot) refreshAll(ctx context.Context) {
	targets := b.subs.UniqueTargets()
	if b.cfg.DefaultTarget != nil {
		// добавляем default target, если его ещё нет
		key := b.cfg.DefaultTarget.Key()
		seen := false
		for _, t := range targets {
			if t.Key() == key {
				seen = true
				break
			}
		}
		if !seen {
			targets = append(targets, *b.cfg.DefaultTarget)
		}
	}

	for _, t := range targets {
		result, err := b.svc.Get(ctx, t, 0) // 0 — форсированный fetch
		if err != nil {
			log.Printf("refresh %s: %v", t.Key(), err)
			continue
		}
		if result.Saved {
			b.notifyChange(t, result.Schedule)
		}
	}
}

// notifyChange шлёт всем подписчикам target'а сообщение об изменении +
// сразу актуальное расписание (обе недели).
func (b *Bot) notifyChange(t model.Target, sched model.Schedule) {
	subscribers := b.subs.Subscribers(t)
	if len(subscribers) == 0 {
		return
	}
	intro := FormatChangeIntro(sched)
	for _, chatID := range subscribers {
		b.sendHTML(chatID, intro)
		for w := range sched.Weeks {
			for _, m := range FormatWeek(sched, w) {
				b.sendHTML(chatID, m)
			}
		}
	}
}

// sendHTML отправляет HTML-сообщение, попутно соблюдая лимит 1 msg/sec на чат.
func (b *Bot) sendHTML(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send %d: %v", chatID, err)
	}
	// Telegram: 1 msg/sec на один чат. Чуть-чуть притормаживаем.
	time.Sleep(60 * time.Millisecond)
}

// reply отправляет ответ на сообщение (тот же chat, plain text).
func (b *Bot) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("send %d: %v", chatID, err)
	}
}

// splitCommand разбивает текст сообщения на команду и аргументы.
// Поддерживает форму "/cmd@botname args" — отрезает @botname.
func splitCommand(text string) (cmd, args string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	cmd = parts[0]
	if at := strings.Index(cmd, "@"); at >= 0 {
		cmd = cmd[:at]
	}
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return
}
