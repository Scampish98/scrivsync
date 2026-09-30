package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"scrivsync/internal/archive"
	"scrivsync/internal/config"
	"scrivsync/internal/lock"
	"scrivsync/internal/sync"
	"scrivsync/internal/yandex"
)

const usage = `scrivsync — ручной обмен проектом через ZIP на Яндекс Диске

Использование:
  scrivsync push
  scrivsync pull [--force]

Пути и токен читаются из configs/config.yaml в папке проекта программы:
  local_path: '/путь/Project.scriv'
  remote_path: 'disk:/Scrivener/Project.zip'
  token: 'ваш-токен'

remote_path поддерживает disk:/ и app:/, например app:/Project.zip.
Программу можно собрать в корень репозитория или в bin/.
Относительный local_path отсчитывается от папки configs/.
Перед запуском закройте Scrivener. Одновременный обмен с двух устройств не поддерживается.
push сохраняет старый архив в соседнюю облачную папку Backups.
pull останавливается при конфликтах; без конфликтов постоянный локальный бэкап не создаётся.
pull --force принимает архив при конфликтах, сохраняя полный бэкап и отчёт.
Повторите ту же команду после сбоя: она продолжит незавершённую замену.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdout); err != nil {
		if _, writeErr := fmt.Fprintln(os.Stderr, "Ошибка:", err); writeErr != nil {
			slog.Error("Не удалось вывести ошибку", "error", writeErr)
		}
		var conflict *archive.ConflictError
		if errors.As(err, &conflict) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprint(out, usage)
		return err
	}
	command, force, err := parseCommand(args)
	if err != nil {
		return err
	}

	configFile, err := config.Filename()
	if err != nil {
		return err
	}

	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}

	local, err := resolveLocalProject(cfg.LocalPath)
	if err != nil {
		return err
	}

	if err := config.CheckOutsideProject(local, configFile); err != nil {
		return err
	}

	remote := cfg.RemotePath
	state := filepath.Join(filepath.Dir(local), "."+filepath.Base(local)+".scrivsync-state")
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}

	unlock, err := lock.Acquire(filepath.Join(state, "lock"))
	if err != nil {
		return err
	}

	defer unlock()
	if err := lock.CheckScrivener(ctx); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	a := &sync.App{
		Disk:        yandex.New(cfg.Token),
		Local:       local,
		Remote:      remote,
		State:       state,
		Out:         out,
		Now:         time.Now,
		CheckClosed: lock.CheckScrivener,
		ForcePull:   force,
	}

	return a.Run(ctx, command)
}

func parseCommand(args []string) (string, bool, error) {
	if len(args) == 1 && (args[0] == "push" || args[0] == "pull") {
		return args[0], false, nil
	}
	if len(args) == 2 && args[0] == "pull" && args[1] == "--force" {
		return "pull", true, nil
	}

	return "", false, fmt.Errorf("ожидается push, pull или pull --force; пути и токен задаются в config.yaml\n%s", usage)
}

func resolveLocalProject(local string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(local))
	if err != nil {
		return "", fmt.Errorf("родительская папка проекта: %w", err)
	}
	local = filepath.Join(parent, filepath.Base(local))
	if local == filepath.Dir(local) || filepath.Base(local) == "." {
		return "", errors.New("нельзя использовать корень диска как проект")
	}
	if fi, e := os.Lstat(local); e == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("локальный проект должен быть обычной папкой, не ссылкой")
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}

	return local, nil
}
