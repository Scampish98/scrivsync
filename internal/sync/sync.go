package sync

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"scrivsync/internal/yandex"
)

type Disk interface {
	Stat(context.Context, string) (*yandex.Resource, error) // nil means confirmed HTTP 404
	Mkdir(context.Context, string) error
	Upload(context.Context, string, string) error   // local file, remote path; no overwrite
	Download(context.Context, string, string) error // remote path, new local file
	Move(context.Context, string, string) error     // never overwrite
}

type App struct {
	Disk                 Disk
	Local, Remote, State string
	Out                  io.Writer
	Now                  func() time.Time
	CheckClosed          func(context.Context) error
	ForcePull            bool
}

func (a *App) log(format string, v ...any) {
	if _, err := fmt.Fprintf(a.Out, format+"\n", v...); err != nil {
		slog.Error("Не удалось вывести сообщение", "error", err)
	}
}

func (a *App) closed(ctx context.Context) error {
	if a.CheckClosed != nil {
		return a.CheckClosed(ctx)
	}

	return nil
}

// Run resumes an interrupted operation before starting a new one.
func (a *App) Run(ctx context.Context, command string) error {
	if command != "push" && command != "pull" {
		return fmt.Errorf("неизвестная команда: %s", command)
	}
	if a.ForcePull && command != "pull" {
		return fmt.Errorf("--force поддерживается только для pull")
	}
	if recovered, err := a.recover(ctx, command); recovered || err != nil {
		return err
	}
	if command == "push" {
		return a.push(ctx)
	}

	return a.pull(ctx)
}
