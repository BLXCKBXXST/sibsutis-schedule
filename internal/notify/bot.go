package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
)

// Subscriber — то, что бот знает о реестре подписок. Спрятано за
// интерфейсом, чтобы не тянуть зависимость watch → notify (или наоборот).
// Реальная реализация — *watch.Registry.
type Subscriber interface {
	Subscribe(t model.Target, chatID int64, now time.Time) (bool, error)
	Unsubscribe(t model.Target, chatID int64) (bool, error)
	TargetsForChat(chatID int64) []model.Target
}

// Bot — long-poll-цикл getUpdates, разбирает команды и пишет в реестр.
type Bot struct {
	Token  string
	Sender Sender
	Reg    Subscriber
	Client *http.Client
}

// NewBot собирает бот. Sender и Reg обязательны; Client опционален.
func NewBot(token string, sender Sender, reg Subscriber) *Bot {
	return &Bot{
		Token:  token,
		Sender: sender,
		Reg:    reg,
		Client: &http.Client{Timeout: 60 * time.Second},
	}
}

// WhoAmI возвращает username бота через Telegram getMe — нужно для
// построения deep-link'а t.me/<username>?start=... на сайте. Делает
// один HTTP-вызов; в случае сетевой ошибки или пустого токена вернёт
// ошибку, и вызывающий должен решить, как реагировать (логировать и
// продолжить без кнопки, например).
func (b *Bot) WhoAmI(ctx context.Context) (string, error) {
	if b.Token == "" {
		return "", fmt.Errorf("пустой токен")
	}
	url := "https://api.telegram.org/bot" + b.Token + "/getMe"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("status %s", resp.Status)
	}
	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if !payload.OK || payload.Result.Username == "" {
		return "", fmt.Errorf("getMe вернул пустой username")
	}
	return payload.Result.Username, nil
}

// Run блокирует горутину, опрашивает /getUpdates до ctx.Done().
// Telegram long-poll: запрос висит до 30 сек, при появлении апдейта
// возвращается список; мы коммитим offset = max(update_id)+1.
func (b *Bot) Run(ctx context.Context) {
	if b.Token == "" {
		log.Printf("notify: TelegramBotToken пустой, бот не запускается")
		return
	}
	log.Printf("notify: бот стартовал")
	var offset int64
	for {
		select {
		case <-ctx.Done():
			log.Printf("notify: бот остановлен")
			return
		default:
		}
		updates, err := b.fetchUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("notify: getUpdates: %v", err)
			// Не штормуем API при ошибке — короткая пауза.
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.handleUpdate(ctx, u)
		}
	}
}

type tgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

func (b *Bot) fetchUpdates(ctx context.Context, offset int64, timeoutSec int) ([]tgUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=%d",
		b.Token, offset, timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	var payload struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, fmt.Errorf("ok=false")
	}
	return payload.Result, nil
}

func (b *Bot) handleUpdate(ctx context.Context, u tgUpdate) {
	if u.Message == nil || u.Message.Text == "" {
		return
	}
	chatID := u.Message.Chat.ID
	text := strings.TrimSpace(u.Message.Text)

	switch {
	case strings.HasPrefix(text, "/start"):
		b.handleStart(ctx, chatID, strings.TrimSpace(strings.TrimPrefix(text, "/start")))
	case text == "/list":
		b.handleList(ctx, chatID)
	case strings.HasPrefix(text, "/unsubscribe"):
		b.handleUnsubscribe(ctx, chatID, strings.TrimSpace(strings.TrimPrefix(text, "/unsubscribe")))
	case text == "/help" || text == "/":
		b.send(ctx, chatID, helpText)
	default:
		b.send(ctx, chatID, "Не понял команду. Доступны: /list, /unsubscribe, /help.")
	}
}

const helpText = "Привет!\n\n" +
	"Я присылаю уведомления, когда твоё расписание СибГУТИ меняется " +
	"(отмена пары, перенос аудитории, замена преподавателя).\n\n" +
	"Чтобы подписаться:\n" +
	"1. Открой сайт «Расписание СибГУТИ» и найди своё расписание;\n" +
	"2. Нажми «Уведомления в Telegram» — откроется этот чат с готовой подпиской.\n\n" +
	"Команды:\n" +
	"/list — твои подписки\n" +
	"/unsubscribe — отписаться от всех\n" +
	"/help — эта справка"

// handleStart обрабатывает /start [token]. Без токена — приветствие;
// с токеном (base64url(type/query)) — добавляет подписку.
func (b *Bot) handleStart(ctx context.Context, chatID int64, arg string) {
	if arg == "" {
		b.send(ctx, chatID, helpText)
		return
	}
	target, err := decodeStartToken(arg)
	if err != nil {
		b.send(ctx, chatID, "Не удалось разобрать подписку. Попробуй с сайта ещё раз.")
		return
	}
	added, err := b.Reg.Subscribe(target, chatID, time.Now())
	if err != nil {
		b.send(ctx, chatID, "Не удалось сохранить подписку: "+err.Error())
		return
	}
	if added {
		b.send(ctx, chatID, fmt.Sprintf("Готово! Подписал тебя на «%s». "+
			"Буду писать, если расписание изменится.", target.Label()))
	} else {
		b.send(ctx, chatID, fmt.Sprintf("Подписка на «%s» уже была активной.", target.Label()))
	}
}

func (b *Bot) handleList(ctx context.Context, chatID int64) {
	targets := b.Reg.TargetsForChat(chatID)
	if len(targets) == 0 {
		b.send(ctx, chatID, "У тебя нет подписок. Открой расписание на сайте и нажми «Подписаться».")
		return
	}
	var sb strings.Builder
	sb.WriteString("Твои подписки:\n")
	for _, t := range targets {
		sb.WriteString("• ")
		sb.WriteString(t.Label())
		sb.WriteString("\n")
	}
	sb.WriteString("\nЧтобы отписаться от всех — /unsubscribe.")
	b.send(ctx, chatID, sb.String())
}

func (b *Bot) handleUnsubscribe(ctx context.Context, chatID int64, _ string) {
	targets := b.Reg.TargetsForChat(chatID)
	if len(targets) == 0 {
		b.send(ctx, chatID, "Подписок не было — отписываться не от чего.")
		return
	}
	for _, t := range targets {
		_, _ = b.Reg.Unsubscribe(t, chatID)
	}
	b.send(ctx, chatID, fmt.Sprintf("Отписал тебя от %d подписок.", len(targets)))
}

func (b *Bot) send(ctx context.Context, chatID int64, text string) {
	if err := b.Sender.Send(ctx, chatID, text); err != nil {
		log.Printf("notify: send to %d: %v", chatID, err)
	}
}

// EncodeStartToken кодирует target в строку, годную для deep-link'а
// `https://t.me/<bot>?start=<token>`. Формат: base64url("type/query").
// Подписи нет — токен общедоступен, любой подписывается «за себя».
func EncodeStartToken(t model.Target) string {
	typ := string(t.Type)
	if t.Type == model.TypeStudent {
		typ = "group"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(typ + "/" + t.Query))
}

func decodeStartToken(s string) (model.Target, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return model.Target{}, fmt.Errorf("bad base64: %w", err)
	}
	typ, q, ok := strings.Cut(string(raw), "/")
	if !ok || q == "" {
		return model.Target{}, fmt.Errorf("bad format")
	}
	var tt model.TargetType
	switch typ {
	case "group":
		tt = model.TypeStudent
	case "teacher":
		tt = model.TypeTeacher
	case "room":
		tt = model.TypeRoom
	default:
		return model.Target{}, fmt.Errorf("unknown type %q", typ)
	}
	return model.Target{Type: tt, Query: q}, nil
}
