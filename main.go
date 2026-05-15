// sibsutis-schedule — парсер расписания с my.sibsutis.ru.
//
// Утилита логинится в личный кабинет (Bitrix CMS), выгружает расписание по
// группе, преподавателю или аудитории, сохраняет каждую выгрузку в локальную
// историю версий и печатает расписание таблицей в терминал. Если сайт
// недоступен — показывает последнюю версию из истории с пометкой.
//
// Команды:
//
//	sibsutis-schedule                          выгрузить и показать (target по умолчанию из config.txt)
//	sibsutis-schedule show   [--group|--teacher|--room <q>]   то же явно; --version <ID> — версия из истории
//	sibsutis-schedule update [--group|--teacher|--room <q>]   выгрузить и сохранить (для cron/systemd timer)
//	sibsutis-schedule history [--group|--teacher|--room <q>]  список target'ов или версий target'а
//	sibsutis-schedule version                  версия утилиты
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BLXCKBXXST/sibsutis-schedule/internal/config"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/model"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/render"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/resolve"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/schedule"
	"github.com/BLXCKBXXST/sibsutis-schedule/internal/store"
)

const version = "0.2.0"

// httpTimeout — общий таймаут на сетевые операции (логин + поиск + выгрузка).
const httpTimeout = 25 * time.Second

func main() {
	if len(os.Args) < 2 {
		os.Exit(runShow(nil)) // поведение по умолчанию — показать расписание
	}

	switch cmd := os.Args[1]; cmd {
	case "show":
		os.Exit(runShow(os.Args[2:]))
	case "update":
		os.Exit(runUpdate(os.Args[2:]))
	case "history":
		os.Exit(runHistory(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Println("sibsutis-schedule", version)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
	default:
		// Флаги без команды трактуем как `show` с этими флагами.
		if strings.HasPrefix(cmd, "-") {
			os.Exit(runShow(os.Args[1:]))
		}
		fmt.Fprintf(os.Stderr, "неизвестная команда: %s\n\n", cmd)
		printUsage(os.Stderr)
		os.Exit(1)
	}
}

// targetFlags — общие для команд флаги выбора target'а.
type targetFlags struct {
	group   *string
	teacher *string
	room    *string
}

func addTargetFlags(fs *flag.FlagSet) targetFlags {
	return targetFlags{
		group:   fs.String("group", "", "расписание группы (напр. ИБ-211)"),
		teacher: fs.String("teacher", "", "расписание преподавателя (ФИО)"),
		room:    fs.String("room", "", "расписание аудитории (напр. 1-101)"),
	}
}

// target определяет target из флагов команды или из target'а по умолчанию в cfg.
// cfg может быть nil — тогда допустимы только флаги.
func (tf targetFlags) target(cfg *config.Config) (model.Target, error) {
	var flagged []model.Target
	if g := strings.TrimSpace(*tf.group); g != "" {
		flagged = append(flagged, model.Target{Type: model.TypeStudent, Query: g})
	}
	if t := strings.TrimSpace(*tf.teacher); t != "" {
		flagged = append(flagged, model.Target{Type: model.TypeTeacher, Query: t})
	}
	if r := strings.TrimSpace(*tf.room); r != "" {
		flagged = append(flagged, model.Target{Type: model.TypeRoom, Query: r})
	}

	switch len(flagged) {
	case 1:
		return flagged[0], nil
	case 0:
		if cfg != nil && cfg.DefaultTarget != nil {
			return *cfg.DefaultTarget, nil
		}
		return model.Target{}, errors.New("target не задан: укажи --group / --teacher / --room " +
			"или пропиши group/teacher/room в config.txt")
	default:
		return model.Target{}, errors.New("укажи только один из --group / --teacher / --room")
	}
}

// runShow выгружает расписание target'а и печатает его. При сетевой ошибке (или
// ошибке конфига) показывает последнюю версию из истории с пометкой. Ошибки
// самого запроса (target не найден / неоднозначен) кэшем не подменяются.
func runShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	configPath := fs.String("config", "", "путь к config.txt")
	dataDir := fs.String("data-dir", "", "каталог истории (по умолчанию ~/.local/share/sibsutis-schedule)")
	noFetch := fs.Bool("no-fetch", false, "не обращаться к сайту, показать последнюю версию из истории")
	versionID := fs.String("version", "", "показать конкретную версию из истории по её ID")
	tf := addTargetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := store.New(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Конфиг нужен и для онлайн-выгрузки, и как источник target'а по умолчанию.
	cfg, cfgErr := config.Load(*configPath)
	cfgForTarget := cfg
	if cfgErr != nil {
		cfgForTarget = nil
	}

	target, err := tf.target(cfgForTarget)
	if err != nil {
		if cfgErr != nil {
			fmt.Fprintln(os.Stderr, "ошибка конфига:", cfgErr)
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	key := target.Key()

	// Конкретная версия из истории.
	if *versionID != "" {
		sched, err := st.Load(key, *versionID)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		render.Schedule(os.Stdout, sched, render.Options{FromCache: true, CacheReason: "версия из истории"})
		return 0
	}

	// Режим только-кэш.
	if *noFetch {
		return showFromHistory(st, key, "режим без обращения к сайту")
	}

	// Онлайн-выгрузка — нужен валидный конфиг с креденшелами.
	if cfgErr != nil {
		fmt.Fprintln(os.Stderr, "ошибка конфига:", cfgErr)
		fmt.Fprintln(os.Stderr, "показываю расписание из истории...")
		return showFromHistory(st, key, "конфиг недоступен")
	}

	svc := newService(cfg, st)
	result, err := svc.Get(context.Background(), target, 0) // CLI всегда форсирует свежий fetch
	if err != nil {
		// Ошибки самого запроса — не подменяем устаревшим кэшем.
		if errors.Is(err, resolve.ErrNotFound) || errors.Is(err, resolve.ErrAmbiguous) {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "не удалось выгрузить расписание:", err)
		fmt.Fprintln(os.Stderr, "показываю расписание из истории...")
		return showFromHistory(st, key, "сайт недоступен")
	}

	render.Schedule(os.Stdout, result.Schedule, render.Options{FromCache: false})
	return 0
}

// runUpdate выгружает и сохраняет расписание target'а. Предназначена для
// cron/systemd timer: печатает только короткий итог, код возврата отражает успех.
func runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	configPath := fs.String("config", "", "путь к config.txt")
	dataDir := fs.String("data-dir", "", "каталог истории")
	quiet := fs.Bool("quiet", false, "печатать только ошибки")
	tf := addTargetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := store.New(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка конфига:", err)
		return 1
	}

	target, err := tf.target(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	svc := newService(cfg, st)
	result, err := svc.Get(context.Background(), target, 0)
	if err != nil {
		if errors.Is(err, resolve.ErrNotFound) || errors.Is(err, resolve.ErrAmbiguous) {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "не удалось выгрузить расписание:", err)
		return 1
	}

	if !*quiet {
		when := result.Schedule.FetchedAt.Local().Format("2006-01-02T15-04-05")
		if result.Saved {
			fmt.Printf("[%s] сохранена новая версия: %s\n", target.Label(), when)
		} else {
			fmt.Printf("[%s] расписание не изменилось (выгружено: %s)\n", target.Label(), when)
		}
	}
	return 0
}

// runHistory без флагов печатает сводку по всем target'ам; с флагом target —
// список версий конкретного target'а.
func runHistory(args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "каталог истории")
	tf := addTargetFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := store.New(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// Если задан флаг target'а — показываем версии этого target'а.
	if *tf.group != "" || *tf.teacher != "" || *tf.room != "" {
		target, err := tf.target(nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return historyForTarget(st, target)
	}

	// Иначе — сводка по всем target'ам.
	summaries, err := st.Targets()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	rows := make([]render.TargetRow, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, render.TargetRow{
			Key: s.Key, Target: s.Target, Versions: s.Versions,
			LatestAt: s.LatestAt, LastCheck: s.LastCheck, LastError: s.LastError,
		})
	}
	render.Targets(os.Stdout, rows)
	fmt.Printf("\nКаталог истории: %s\n", st.Dir())
	return 0
}

// historyForTarget печатает список версий конкретного target'а.
func historyForTarget(st *store.Store, target model.Target) int {
	key := target.Key()
	list, err := st.List(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("История: %s\n\n", target.Label())
	rows := make([]render.VersionRow, 0, len(list))
	for _, v := range list {
		rows = append(rows, render.VersionRow{
			ID: v.ID, FetchedAt: v.FetchedAt, Title: v.Title, Lessons: v.Lessons,
		})
	}
	render.Versions(os.Stdout, rows)

	if m, err := st.ReadMeta(key); err == nil && !m.LastCheck.IsZero() {
		fmt.Printf("\nПоследняя проверка: %s\n", m.LastCheck.Local().Format("02.01.2006 15:04"))
		if m.LastError != "" {
			fmt.Printf("Последняя ошибка:   %s\n", m.LastError)
		}
	}
	fmt.Printf("\nКаталог истории: %s\n", st.Dir())
	return 0
}

// newService собирает schedule.Service для онлайн-выгрузки + сохранения версии.
func newService(cfg *config.Config, st *store.Store) *schedule.Service {
	return schedule.New(&schedule.HTTPFetcher{Cfg: cfg, Timeout: httpTimeout}, st)
}

// showFromHistory печатает последнюю сохранённую версию расписания target'а.
func showFromHistory(st *store.Store, key, reason string) int {
	sched, _, err := st.Latest(key)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	render.Schedule(os.Stdout, sched, render.Options{FromCache: true, CacheReason: reason})
	return 0
}

func printUsage(w *os.File) {
	fmt.Fprint(w, `sibsutis-schedule — парсер расписания с my.sibsutis.ru

Использование:
  sibsutis-schedule [команда] [флаги]

Команды:
  show       выгрузить и показать расписание; при сбое — из истории (по умолчанию)
  update     выгрузить и сохранить расписание (для cron / systemd timer)
  history    список target'ов в истории или версий конкретного target'а
  version    показать версию утилиты
  help       показать эту справку

Выбор target'а (для show / update / history) — ровно один флаг, иначе берётся
target по умолчанию из config.txt:
  --group <название>     расписание группы, напр. --group ИБ-211
  --teacher <ФИО>        расписание преподавателя, напр. --teacher "Иванов И.И."
  --room <аудитория>     расписание аудитории, напр. --room 1-101

Флаги show:
  --config <путь>    путь к config.txt (по умолчанию ./config.txt и
                     ~/.config/sibsutis-schedule/config.txt)
  --data-dir <путь>  каталог истории (по умолчанию ~/.local/share/sibsutis-schedule)
  --no-fetch         не обращаться к сайту, показать последнюю версию из истории
  --version <ID>     показать конкретную версию из истории (ID см. в `+"`history`"+`)

Флаги update:
  --config <путь>    путь к config.txt
  --data-dir <путь>  каталог истории
  --quiet            печатать только ошибки

Конфиг (config.txt, формат key=value):
  login=ваш_логин
  password=ваш_пароль
  group=ИБ-211       # target по умолчанию: один из group / teacher / room
`)
}
