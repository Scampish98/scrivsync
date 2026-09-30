package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestConfigYAMLAndRelativePath(t *testing.T) {
	t.Setenv("YANDEX_DISK_SCRIVENER_TOKEN", "obsolete-env-must-not-be-used")
	filename := writeConfig(t, "# Настройки\nlocal_path: '../Мой роман.scriv'\nremote_path: 'disk:/Романы/Мой роман.zip'\ntoken: 'token-from-config#literal' # comment\n")
	cfg, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "token-from-config#literal" || cfg.RemotePath != "disk:/Романы/Мой роман.zip" {
		t.Fatal("YAML strings parsed incorrectly")
	}
	want := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "Мой роман.scriv"))
	if cfg.LocalPath != want {
		t.Fatalf("relative path resolved against wrong directory: %q, want %q", cfg.LocalPath, want)
	}
}

func TestConfigLocationForRootAndBinExecutables(t *testing.T) {
	for _, subdir := range []string{"", "bin"} {
		t.Run("executable-in-"+subdir, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, subdir)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			program := filepath.Join(dir, "scrivsync")
			if err := os.WriteFile(program, nil, 0700); err != nil {
				t.Fatal(err)
			}
			got, err := configForExecutable(program)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(resolved, "configs", "config.yaml"); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			link := filepath.Join(t.TempDir(), "shortcut")
			if err := os.Symlink(program, link); err != nil {
				t.Skip(err)
			}
			viaLink, err := configForExecutable(link)
			if err != nil || viaLink != got {
				t.Fatalf("symlink resolved to wrong config: %q, %v", viaLink, err)
			}
		})
	}
}

func TestConfigRejectsInvalidInputWithoutLeakingToken(t *testing.T) {
	valid := "local_path: 'Project.scriv'\nremote_path: 'disk:/Project.zip'\ntoken: 'secret-config-token'\n"
	for name, body := range map[string]string{
		"empty":              "",
		"missing token":      "local_path: 'Project.scriv'\nremote_path: 'disk:/Project.zip'\n",
		"blank token":        strings.ReplaceAll(valid, "'secret-config-token'", "''"),
		"unknown key":        valid + "secret-config-token: ignored\n",
		"duplicate key":      valid + "token: secret-config-token\n",
		"invalid syntax":     "token: [secret-config-token\n",
		"wrong type":         strings.ReplaceAll(valid, "'secret-config-token'", "[secret-config-token]"),
		"multiple documents": valid + "---\ntoken: secret-config-token\n",
		"token newline":      strings.ReplaceAll(valid, "'secret-config-token'", "\"secret-config-token\\n\""),
		"null token":         strings.ReplaceAll(valid, "'secret-config-token'", "null"),
		"wrong remote":       strings.ReplaceAll(valid, "disk:/Project.zip", "disk:/Project/"),
		"tilde":              strings.ReplaceAll(valid, "'Project.scriv'", "'~/Project.scriv'"),
	} {
		t.Run(name, func(t *testing.T) {
			// A legacy environment token must not rescue a missing/invalid config token.
			t.Setenv("YANDEX_DISK_SCRIVENER_TOKEN", "obsolete-env-must-not-be-used")
			_, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatal("invalid config accepted")
			}
			if strings.Contains(err.Error(), "secret-config-token") || strings.Contains(err.Error(), "obsolete-env-must-not-be-used") {
				t.Fatal("secret leaked through config validation")
			}
		})
	}
}

func TestMissingConfigExplainsWhereToCreateIt(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.yaml")
	_, err := Load(filename)
	if err == nil || !strings.Contains(err.Error(), filename) {
		t.Fatalf("missing actionable error: %v", err)
	}
}

func TestConfigCannotBePackedInsideProject(t *testing.T) {
	project := t.TempDir()
	inside := filepath.Join(project, "config.yaml")
	if err := os.WriteFile(inside, []byte("token: secret"), 0600); err != nil {
		t.Fatal(err)
	}
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckOutsideProject(resolvedProject, inside); err == nil {
		t.Fatal("config inside project would be archived")
	}
	outside := writeConfig(t, "token: secret")
	if err := CheckOutsideProject(resolvedProject, outside); err != nil {
		t.Fatal(err)
	}
}

func TestRemotePathValidation(t *testing.T) {
	for _, p := range []string{"disk:/a/../b.zip", "disk:/folder/", "https://example.org/a.zip", "disk:/a\\b.zip"} {
		if _, err := remotePath(p); err == nil {
			t.Fatalf("accepted %q", p)
		}
	}
}
