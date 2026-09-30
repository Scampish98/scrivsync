//go:build darwin || linux

package lock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"

	"scrivsync/internal/fileutil"
)

func Acquire(p string) (func(), error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		fileutil.CloseFileOnReturn(&f)
		return nil, fmt.Errorf("проект уже синхронизируется или блокировка недоступна: %w", err)
	}

	return func() {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			slog.Error("Не удалось снять блокировку проекта", "path", p, "error", err)
		}
		fileutil.CloseFileOnReturn(&f)
	}, nil
}

func CheckScrivener(ctx context.Context) error {
	err := exec.CommandContext(ctx, "/usr/bin/pgrep", "-x", "Scrivener").Run()
	if err == nil {
		return errors.New("scrivener запущен; закройте приложение перед обменом")
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == 1 {
		return nil
	}

	return fmt.Errorf("не удалось проверить, закрыт ли Scrivener: %w", err)
}
