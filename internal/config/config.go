package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"scrivsync/internal/fileutil"
)

type Config struct {
	LocalPath  string
	RemotePath string
	Token      string
}

func configForExecutable(executable string) (string, error) {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("не удалось определить папку программы: %w", err)
	}

	root := filepath.Dir(resolved)
	if strings.EqualFold(filepath.Base(root), "bin") {
		root = filepath.Dir(root)
	}

	return filepath.Join(root, "configs", "config.yaml"), nil
}

func Filename() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", errors.New("не удалось определить расположение программы")
	}

	return configForExecutable(executable)
}

func Load(filename string) (Config, error) {
	data, err := readConfig(filename)
	if err != nil {
		return Config{}, err
	}

	entries, err := decodeDocument(data)
	if err != nil {
		return Config{}, err
	}

	cfg, err := parseFields(entries)
	if err != nil {
		return Config{}, err
	}

	return cfg.resolve(filename)
}

func readConfig(filename string) ([]byte, error) {
	f, err := os.Open(filename)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("не найден %q; скопируйте configs/config.example.yaml в configs/config.yaml и заполните настройки", filename)
	}
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть config.yaml: %w", err)
	}

	defer fileutil.CloseFileOnReturn(&f)
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return nil, errors.New("не удалось прочитать config.yaml")
	}
	if len(b) > 64<<10 {
		return nil, errors.New("config.yaml превышает 64 КиБ")
	}

	return b, nil
}

func decodeDocument(b []byte) ([]*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	var document yaml.Node
	// Parser errors may include source text with the token, so never forward them.
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("неверный YAML в config.yaml; проверьте отступы и кавычки")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("config.yaml должен содержать ровно один YAML-документ")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config.yaml должен содержать поля local_path, remote_path и token")
	}

	return document.Content[0].Content, nil
}

func parseFields(entries []*yaml.Node) (Config, error) {
	var cfg Config
	seen := map[string]bool{}
	for i := 0; i < len(entries); i += 2 {
		key, value := entries[i], entries[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return cfg, errors.New("неверное имя поля в config.yaml")
		}
		switch key.Value {
		case "local_path", "remote_path", "token":
		default:
			return cfg, errors.New("неизвестное поле в config.yaml; допустимы только local_path, remote_path и token")
		}
		if seen[key.Value] {
			return cfg, fmt.Errorf("поле %s указано в config.yaml несколько раз", key.Value)
		}
		seen[key.Value] = true
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return cfg, fmt.Errorf("поле %s в config.yaml должно быть строкой в кавычках", key.Value)
		}
		if strings.TrimSpace(value.Value) == "" {
			return cfg, fmt.Errorf("заполните поле %s в config.yaml", key.Value)
		}
		switch key.Value {
		case "local_path":
			cfg.LocalPath = value.Value
		case "remote_path":
			cfg.RemotePath = value.Value
		case "token":
			cfg.Token = value.Value
		}
	}

	for _, key := range []string{"local_path", "remote_path", "token"} {
		if !seen[key] {
			return cfg, fmt.Errorf("в config.yaml отсутствует поле %s", key)
		}
	}

	return cfg, nil
}

func (cfg Config) resolve(filename string) (Config, error) {
	if strings.ContainsAny(cfg.Token, " \t\r\n\x00") || strings.TrimSpace(cfg.Token) != cfg.Token {
		return cfg, errors.New("поле token должно содержать токен без пробелов и переносов строк")
	}
	if strings.ContainsAny(cfg.LocalPath, "\x00\r\n") {
		return cfg, errors.New("недопустимые символы в local_path")
	}
	if strings.HasPrefix(cfg.LocalPath, "~") {
		return cfg, errors.New("в local_path укажите полный путь или путь относительно config.yaml; сокращение ~ не поддерживается")
	}
	if !filepath.IsAbs(cfg.LocalPath) {
		cfg.LocalPath = filepath.Join(filepath.Dir(filename), cfg.LocalPath)
	}
	var err error
	cfg.LocalPath, err = filepath.Abs(cfg.LocalPath)
	if err != nil {
		return Config{}, errors.New("не удалось разрешить local_path")
	}
	cfg.RemotePath, err = remotePath(cfg.RemotePath)
	if err != nil {
		return Config{}, fmt.Errorf("remote_path в config.yaml: %w", err)
	}

	return cfg, nil
}

func CheckOutsideProject(local, config string) error {
	resolved, err := filepath.EvalSymlinks(config)
	if err != nil {
		return fmt.Errorf("не удалось проверить расположение config.yaml: %w", err)
	}

	rel, err := filepath.Rel(local, resolved)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return errors.New("config.yaml должен находиться вне папки проекта, чтобы токен не попал в архив")
	}

	// File identity also covers aliases differing in case on macOS/Windows.
	if project, err := os.Stat(local); err == nil {
		for dir := filepath.Dir(resolved); ; dir = filepath.Dir(dir) {
			info, err := os.Stat(dir)
			if err != nil {
				return errors.New("не удалось проверить расположение config.yaml")
			}
			if os.SameFile(project, info) {
				return errors.New("config.yaml должен находиться вне папки проекта, чтобы токен не попал в архив")
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}

	return nil
}

func remotePath(p string) (string, error) {
	if strings.HasPrefix(p, "/") {
		p = "disk:" + p
	}
	if !strings.HasPrefix(p, "disk:/") || strings.ContainsAny(p, "\\\x00\r\n") {
		return "", errors.New("путь архива должен иметь вид disk:/папка/Project.zip")
	}

	for _, part := range strings.Split(strings.TrimPrefix(p, "disk:/"), "/") {
		if part == ".." || part == "." || part == "" {
			return "", errors.New("облачный путь содержит пустой компонент, . или ..")
		}
	}
	if !strings.EqualFold(path.Ext(p), ".zip") {
		return "", errors.New("облачный путь должен указывать на .zip, а не папку")
	}

	return p, nil
}
