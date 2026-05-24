// sibsutis-schedule-web — HTTP-сервер расписания SibSUTI.
//
// Долгоиграющий процесс, слушает на адресе из config.txt (web_listen_addr,
// default :8080). Переиспользует internal/schedule.Service для кэширования
// и singleflight'а, чтобы не лупить my.sibsutis.ru на каждый запрос.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/watch"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/web"
)

const version = "0.3.0"

func main() {
	configPath := flag.String("config", "", "путь к config.txt")
	dataDir := flag.String("data-dir", "", "каталог истории (по умолчанию ~/.local/share/sibsutis-schedule)")
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println("sibsutis-schedule-web", version)
		return
	}

	if err := run(*configPath, *dataDir); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Println("web: stopped")
			return
		}
		log.Fatalf("web: %v", err)
	}
}

func run(configPath, dataDir string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	st, err := store.New(dataDir)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	svc := schedule.New(&schedule.HTTPFetcher{Cfg: cfg}, st)

	srv, err := web.New(cfg, svc, st)
	if err != nil {
		return fmt.Errorf("web server: %w", err)
	}

	reg, err := watch.Open(filepath.Join(st.Dir(), "watch.json"))
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	worker := watch.NewWorker(reg, svc, cfg.WatchInterval, cfg.WatchTTL)
	srv.SetTouchHook(worker.TouchHook())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Воркер живёт всю жизнь процесса, останавливается на ctx.Done().
	go worker.Run(ctx)

	return srv.ListenAndServe(ctx)
}
