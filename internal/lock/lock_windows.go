//go:build windows

package lock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
)

func Acquire(p string) (func(), error) {
	u, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return nil, err
	}

	h, err := syscall.CreateFile(u, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("проект уже синхронизируется или блокировка недоступна: %w", err)
	}

	return func() {
		if err := syscall.CloseHandle(h); err != nil {
			slog.Error("Не удалось закрыть блокировку проекта", "path", p, "error", err)
		}
	}, nil
}

func CheckScrivener(ctx context.Context) error {
	b, err := exec.CommandContext(ctx, "tasklist.exe", "/FI", "IMAGENAME eq Scrivener.exe", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return fmt.Errorf("не удалось проверить, закрыт ли Scrivener: %w", err)
	}
	if strings.Contains(strings.ToLower(string(b)), "scrivener.exe") {
		return errors.New("Scrivener запущен; закройте приложение перед обменом")
	}

	return nil
}
