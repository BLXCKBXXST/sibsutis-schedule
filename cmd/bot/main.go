// sibsutis-schedule-bot — Telegram-бот поверх парсера расписания.
//
// Долгоиграющий процесс: long-polling Telegram API + фоновое обновление
// расписаний, на которые подписаны пользователи. Конфиг тот же, что и у CLI,
// плюс ключи telegram_token и (опц.) cycle_anchor, telegram_freshness_minutes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/bot"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/subs"
)

const version = "0.2.0"

func main() {
	configPath := flag.String("config", "", "путь к config.txt")
	dataDir := flag.String("data-dir", "", "каталог истории и подписок")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("sibsutis-schedule-bot", version)
		return
	}

	if err := run(*configPath, *dataDir); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Println("bot остановлен")
			return
		}
		log.Fatalf("bot: %v", err)
	}
}

func run(configPath, dataDir string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.TelegramToken == "" {
		return errors.New("в config.txt не задан telegram_token (получи у @BotFather)")
	}

	st, err := store.New(dataDir)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	ss, err := subs.New(st.Dir())
	if err != nil {
		return fmt.Errorf("subs: %w", err)
	}

	api, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return fmt.Errorf("telegram api: %w", err)
	}

	svc := schedule.New(&schedule.HTTPFetcher{Cfg: cfg}, st)
	b := bot.New(api, cfg, svc, st, ss)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return b.Run(ctx)
}
