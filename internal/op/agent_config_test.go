package op

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 临时 CODEX_HOME 环境：注册表按 env 解析路径，测试内直接操作真实注册表
func setTestCodexHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	return dir
}

func TestAgentConfigListAndSaveRoundTrip(t *testing.T) {
	setTestCodexHome(t)

	files, err := AgentConfigList("codex")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 config files (config/auth), got %d", len(files))
	}
	for _, f := range files {
		if f.Exists {
			t.Fatalf("expected %s to not exist initially", f.Key)
		}
		if f.Content != "" {
			t.Fatalf("expected empty content for missing file %s", f.Key)
		}
	}

	err = AgentConfigSave("codex", []AgentConfigSaveInput{
		{Key: "config", Content: "model = \"gpt-test\"\n"},
		{Key: "auth", Content: `{"OPENAI_API_KEY":"sk-test"}`},
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	files, err = AgentConfigList("codex")
	if err != nil {
		t.Fatalf("list after save: %v", err)
	}
	byKey := map[string]AgentConfigFileView{}
	for _, f := range files {
		byKey[f.Key] = f
	}
	if !byKey["config"].Exists || byKey["config"].Content != "model = \"gpt-test\"\n" {
		t.Fatalf("config.toml round-trip failed: %+v", byKey["config"])
	}
	if !byKey["auth"].Exists || !strings.Contains(byKey["auth"].Content, "sk-test") {
		t.Fatalf("auth.json round-trip failed: %+v", byKey["auth"])
	}
	if byKey["config"].ModifiedAt == 0 {
		t.Fatalf("expected non-zero mtime after save")
	}
}

func TestAgentConfigSaveValidation(t *testing.T) {
	setTestCodexHome(t)

	cases := []struct {
		name    string
		key     string
		content string
	}{
		{"bad toml", "config", "model = \"unterminated\nbroken["},
		{"bad json", "auth", "{not json"},
		{"json non-object", "auth", "[1,2,3]"},
	}
	for _, tc := range cases {
		err := AgentConfigSave("codex", []AgentConfigSaveInput{{Key: tc.key, Content: tc.content}})
		if err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
		if errors.Is(err, ErrAgentConfigConflict) {
			t.Fatalf("%s: unexpected conflict error", tc.name)
		}
	}
}

func TestAgentConfigSavePreflightsAllFiles(t *testing.T) {
	for _, scenario := range []string{"invalid auth", "auth conflict", "duplicate key", "deleted auth"} {
		t.Run(scenario, func(t *testing.T) {
			dir := setTestCodexHome(t)
			configPath := filepath.Join(dir, "config.toml")
			authPath := filepath.Join(dir, "auth.json")
			original := "model = \"original\"\n"
			if err := os.WriteFile(configPath, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(authPath, []byte(`{"OPENAI_API_KEY":"old"}`), 0600); err != nil {
				t.Fatal(err)
			}
			views, err := AgentConfigList("codex")
			if err != nil {
				t.Fatal(err)
			}
			byKey := map[string]AgentConfigFileView{}
			for _, view := range views {
				byKey[view.Key] = view
			}
			inputs := []AgentConfigSaveInput{
				{Key: "config", Content: "model = \"updated\"\n", BaseModifiedAt: byKey["config"].ModifiedAt},
				{Key: "auth", Content: `{"OPENAI_API_KEY":"new"}`, BaseModifiedAt: byKey["auth"].ModifiedAt},
			}
			switch scenario {
			case "invalid auth":
				inputs[1].Content = "{invalid"
			case "auth conflict":
				future := time.Now().Add(time.Second)
				if err := os.Chtimes(authPath, future, future); err != nil {
					t.Fatal(err)
				}
			case "duplicate key":
				inputs[1] = inputs[0]
			case "deleted auth":
				if err := os.Remove(authPath); err != nil {
					t.Fatal(err)
				}
			}
			if err := AgentConfigSave("codex", inputs); err == nil {
				t.Fatal("invalid batch must be rejected")
			}
			got, err := os.ReadFile(configPath)
			if err != nil || string(got) != original {
				t.Fatal("a later file error must not partially save the first file")
			}
			if _, err := os.Stat(configPath + ".bak"); !os.IsNotExist(err) {
				t.Fatal("preflight errors must not start backups or writes")
			}
		})
	}
}

func TestAgentConfigSaveUnknownKeyRejected(t *testing.T) {
	setTestCodexHome(t)
	err := AgentConfigSave("codex", []AgentConfigSaveInput{{Key: "../../evil", Content: "x"}})
	if err == nil {
		t.Fatalf("expected unknown key rejection")
	}
	if errors.Is(err, ErrAgentConfigConflict) {
		t.Fatalf("unexpected conflict error")
	}
}

func TestAgentConfigBackupCreated(t *testing.T) {
	dir := setTestCodexHome(t)
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"old\"\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	files, err := AgentConfigList("codex")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var baseModifiedAt int64
	for _, f := range files {
		if f.Key == "config" {
			baseModifiedAt = f.ModifiedAt
		}
	}

	if err := AgentConfigSave("codex", []AgentConfigSaveInput{{Key: "config", Content: "model = \"new\"\n", BaseModifiedAt: baseModifiedAt}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil || string(data) != "model = \"new\"\n" {
		t.Fatalf("config not saved: %v %q", err, string(data))
	}
	backup, err := os.ReadFile(configPath + ".bak")
	if err != nil || string(backup) != "model = \"old\"\n" {
		t.Fatalf("backup missing or wrong: %v %q", err, string(backup))
	}
}

func TestAgentConfigConflictDetection(t *testing.T) {
	dir := setTestCodexHome(t)
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("model = \"v1\"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	files, err := AgentConfigList("codex")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var baseModifiedAt int64
	for _, f := range files {
		if f.Key == "config" {
			baseModifiedAt = f.ModifiedAt
		}
	}

	// 模拟外部修改（推进 mtime）
	if err := os.WriteFile(configPath, []byte("model = \"external\"\n"), 0644); err != nil {
		t.Fatalf("external edit: %v", err)
	}
	externalTime := time.UnixMilli(baseModifiedAt + 60000)
	if err := os.Chtimes(configPath, externalTime, externalTime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	err = AgentConfigSave("codex", []AgentConfigSaveInput{{Key: "config", Content: "model = \"mine\"\n", BaseModifiedAt: baseModifiedAt}})
	if !errors.Is(err, ErrAgentConfigConflict) {
		t.Fatalf("expected conflict error, got %v", err)
	}
	data, _ := os.ReadFile(configPath)
	if string(data) != "model = \"external\"\n" {
		t.Fatalf("external content must be preserved, got %q", string(data))
	}

	// 携带最新 mtime 保存成功
	err = AgentConfigSave("codex", []AgentConfigSaveInput{{Key: "config", Content: "model = \"mine\"\n", BaseModifiedAt: baseModifiedAt + 60000}})
	if err != nil {
		t.Fatalf("save with fresh mtime: %v", err)
	}
}

func TestAgentConfigUnsupportedAgent(t *testing.T) {
	setTestCodexHome(t)
	if _, err := AgentConfigList("zcode"); err == nil {
		t.Fatalf("expected error for agent without editable config files")
	}
	if err := AgentConfigSave("zcode", []AgentConfigSaveInput{{Key: "config", Content: "x"}}); err == nil {
		t.Fatalf("expected save error for agent without editable config files")
	}
}
