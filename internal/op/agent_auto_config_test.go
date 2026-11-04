package op

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	toml "github.com/pelletier/go-toml/v2"
)

// setTestGateway 设定网关地址来源（conf 未在测试中初始化，需显式设置）
func setTestGateway() {
	conf.AppConfig.Server.Host = "127.0.0.1"
	conf.AppConfig.Server.Port = 18777
}

func TestPatchCodexTomlReplacesExistingTableAndPreservesRest(t *testing.T) {
	content := `# 我的 codex 配置
model_provider = "custom"
model = "gpt-6.1"

[model_providers.custom]
name = "old-name"
base_url = "https://old.example.com/v1"
wire_api = "chat"
requires_openai_auth = true

[mcp_servers.filesystem]
command = "npx"
`
	got := patchCodexToml(content, "sk-octopus-123", "http://127.0.0.1:18777/v1")

	expectations := []string{
		`model_provider = "custom"`,
		`experimental_bearer_token = "sk-octopus-123"`,
		`name = "octopus"`,
		`base_url = "http://127.0.0.1:18777/v1"`,
		`wire_api = "responses"`,
		`requires_openai_auth = false`,
		`model = "gpt-6.1"`,
		`# 我的 codex 配置`,
		`[mcp_servers.filesystem]`,
		`command = "npx"`,
	}
	for _, want := range expectations {
		if !strings.Contains(got, want) {
			t.Fatalf("patched toml missing %q:\n%s", want, got)
		}
	}
	// 不该再出现的旧值
	for _, gone := range []string{"old-name", "https://old.example.com", `"chat"`, "requires_openai_auth = true"} {
		if strings.Contains(got, gone) {
			t.Fatalf("patched toml should not contain %q:\n%s", gone, got)
		}
	}
	// 顶层键必须在任何表格之前；bearer 只允许存在于表内
	if strings.Index(got, "model_provider") > strings.Index(got, "[model_providers.custom]") {
		t.Fatalf("top-level keys must precede tables:\n%s", got)
	}
	beforeTable := got[:strings.Index(got, "[model_providers.custom]")]
	if strings.Contains(beforeTable, "experimental_bearer_token") {
		t.Fatalf("bearer must only exist inside the table:\n%s", got)
	}
}

func TestPatchCodexTomlAppendsMissingTableAndKeys(t *testing.T) {
	content := `model = "gpt-6.1"
`
	got := patchCodexToml(content, "sk-key", "http://127.0.0.1:18777/v1")

	for _, want := range []string{
		`model = "gpt-6.1"`,
		`model_provider = "custom"`,
		`experimental_bearer_token = "sk-key"`,
		`[model_providers.custom]`,
		`base_url = "http://127.0.0.1:18777/v1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "model_provider") > strings.Index(got, "[model_providers.custom]") {
		t.Fatalf("top-level keys must precede tables:\n%s", got)
	}
}

func TestPatchCodexTomlEmptyContent(t *testing.T) {
	got := patchCodexToml("", "sk-key", "http://127.0.0.1:18777/v1")
	for _, want := range []string{
		`model_provider = "custom"`,
		`experimental_bearer_token = "sk-key"`,
		`[model_providers.custom]`,
		`requires_openai_auth = false`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
}

func TestPatchCodexTomlRemovesTopLevelBearer(t *testing.T) {
	// 顶层 bearer 不是 codex 标准配置键，会导致桌面端不带认证头，补丁必须移除
	content := `model_provider = "custom"
model = "gpt-6.1"
experimental_bearer_token = "sk-stale-top-level"

[model_providers.custom]
name = "octopus"
base_url = "http://127.0.0.1:18777/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-in-table"
`
	got := patchCodexToml(content, "sk-fresh", "http://127.0.0.1:18777/v1")
	if strings.Contains(got, "sk-stale-top-level") {
		t.Fatalf("top-level stale bearer must be removed:\n%s", got)
	}
	beforeTable := got[:strings.Index(got, "[model_providers.custom]")]
	if strings.Contains(beforeTable, "experimental_bearer_token") {
		t.Fatalf("bearer must only exist inside the table:\n%s", got)
	}
	if !strings.Contains(got, `experimental_bearer_token = "sk-fresh"`) {
		t.Fatalf("table bearer must be refreshed:\n%s", got)
	}
	if !strings.Contains(got, `model = "gpt-6.1"`) {
		t.Fatalf("user keys must be preserved:\n%s", got)
	}
}

func TestCodexModelPatchPreservesOtherTables(t *testing.T) {
	content := `# Codex settings
model = "old-default"
model_reasoning_effort = "max"
model_context_window = 1000000

[model_providers.other]
base_url = "https://other.example.com"
experimental_bearer_token = "keep-other-token"

[profiles.review]
model = "keep-profile-model"
model_provider = "other"

[mcp_servers.filesystem]
command = "node"
`
	got := patchCodexTomlModel(content, "sk-new", "http://127.0.0.1:18777/v1", "route-default")
	for _, want := range []string{
		`model = "route-default"`, `model_reasoning_effort = "max"`, `model_context_window = 1000000`,
		`experimental_bearer_token = "keep-other-token"`, `model = "keep-profile-model"`,
		`model_provider = "other"`, `[mcp_servers.filesystem]`, `command = "node"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Codex patch should preserve %q:\n%s", want, got)
		}
	}
	if err := validateAgentConfigFile("toml", got); err != nil {
		t.Fatalf("generated TOML is invalid: %v", err)
	}
}

func TestAgentAutoConfigPrepareCodexModelGroup(t *testing.T) {
	setTestGateway()
	dir := setTestCodexHome(t)
	keyID := seedAutoConfigKey(t)
	seedClaudeModelGroups(t)
	original := "# keep comment\r\nmodel = \"original-model\"\r\nmodel_reasoning_effort = \"max\"\r\n"
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fileKey, content, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{
		Agent: "codex", APIKeyID: keyID, CodexModelGroupID: 1999001,
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fileKey != "config" || !strings.Contains(content, `model = "route-default"`) {
		t.Fatalf("expected the selected group alias in Codex config: %s", content)
	}
	if !strings.Contains(content, "\r\n") || !strings.Contains(content, `model_reasoning_effort = "max"`) {
		t.Fatal("Codex formatting and unselected settings must be preserved")
	}
	disk, _ := os.ReadFile(path)
	if string(disk) != original {
		t.Fatal("preparing Codex configuration must not write the file")
	}
	key, _ := APIKeyGet(keyID, context.Background())
	originalKey := key
	t.Cleanup(func() { apiKeyCache.Set(keyID, originalKey) })
	key.SupportedModels = "route-opus"
	apiKeyCache.Set(keyID, key)
	if _, _, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: "codex", APIKeyID: keyID, CodexModelGroupID: 1999001}, context.Background()); err == nil {
		t.Fatal("Codex must reject a model group not allowed by the selected key")
	}
	for _, req := range []model.AgentAutoConfigRequest{
		{Agent: "codex", APIKeyID: keyID, CodexModelGroupID: -1},
		{Agent: "codex", APIKeyID: keyID, CodexModelGroupID: 1999099},
		{Agent: "claude-code", APIKeyID: keyID, CodexModelGroupID: 1999001},
	} {
		if _, _, err := AgentAutoConfigPrepare(req, context.Background()); err == nil {
			t.Fatal("invalid Codex model selection must be rejected")
		}
	}
}

func TestMergeClaudeSettingsPreservesExistingFields(t *testing.T) {
	content := `{
  "model": "opusplan",
  "permissions": { "allow": ["Bash"] },
  "env": { "CUSTOM_FLAG": "1", "ANTHROPIC_AUTH_TOKEN": "old", "ANTHROPIC_DEFAULT_SONNET_MODEL": "custom-sonnet[1M]", "ANTHROPIC_DEFAULT_FABLE_MODEL": "custom-fable" }
}`
	got, err := mergeClaudeSettings(content, "sk-new", "http://127.0.0.1:18777")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	for _, want := range []string{
		`"ANTHROPIC_BASE_URL": "http://127.0.0.1:18777"`,
		`"ANTHROPIC_AUTH_TOKEN": "sk-new"`,
		`"CUSTOM_FLAG": "1"`,
		`"permissions"`,
		`"model": "opusplan"`,
		`"ANTHROPIC_DEFAULT_SONNET_MODEL": "custom-sonnet[1M]"`,
		`"ANTHROPIC_DEFAULT_FABLE_MODEL": "custom-fable"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged settings missing %q:\n%s", want, got)
		}
	}
}

func TestAgentAutoConfigPrepareClaudeModelGroups(t *testing.T) {
	setTestGateway()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	keyID := seedAutoConfigKey(t)
	seedClaudeModelGroups(t)
	original := `{"permissions":{"allow":["Bash"]},"opaque_counter":9007199254740993,"env":{"CUSTOM_FLAG":"1","ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":"old label","ANTHROPIC_SMALL_FAST_MODEL":"old-fast"}}`
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fileKey, content, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{
		Agent: "claude-code", APIKeyID: keyID,
		ModelGroups: &model.ClaudeCodeModelGroups{
			Default: 1999001, Opus: 1999002, Sonnet: 1999001, Haiku: 1999003,
			Fable: 1999002, Subagent: 1999003,
		},
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fileKey != "settings" {
		t.Fatalf("unexpected file key %q", fileKey)
	}
	var result struct {
		Model string            `json:"model"`
		Env   map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.Model != "route-default" {
		t.Fatalf("default model = %q, want group alias", result.Model)
	}
	expected := map[string]string{
		"ANTHROPIC_MODEL":                   "route-default",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":      "route-opus",
		"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "route-opus",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":    "route-default",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":     "route-fast",
		"ANTHROPIC_DEFAULT_FABLE_MODEL":     "route-opus",
		"CLAUDE_CODE_SUBAGENT_MODEL":        "route-fast",
		"ANTHROPIC_SMALL_FAST_MODEL":        "route-fast",
		"CUSTOM_FLAG":                       "1",
		"ANTHROPIC_AUTH_TOKEN":              "sk-auto-test",
		"ANTHROPIC_BASE_URL":                "http://127.0.0.1:18777",
	}
	for key, value := range expected {
		if result.Env[key] != value {
			t.Fatalf("%s = %q, want %q", key, result.Env[key], value)
		}
	}
	if !strings.Contains(content, "9007199254740993") || !strings.Contains(content, `"permissions"`) {
		t.Fatalf("unowned settings were not preserved: %s", content)
	}
	if strings.Contains(content, "upstream-only-model") {
		t.Fatalf("must write the group alias rather than an upstream member model")
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != original {
		t.Fatal("preparing a config must not change the file")
	}
	views, err := AgentConfigList("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if err := AgentConfigSave("claude-code", []AgentConfigSaveInput{{Key: fileKey, Content: content, BaseModifiedAt: views[0].ModifiedAt}}); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil || string(backup) != original {
		t.Fatal("saving the generated config must back up the original")
	}
}

func TestAgentAutoConfigPrepareUsesEditorDraft(t *testing.T) {
	setTestGateway()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	keyID := seedAutoConfigKey(t)
	seedClaudeModelGroups(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"env":{"DISK_ONLY":"1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	draft := `{"permissions":{"allow":["Read"]},"env":{"DRAFT_ONLY":"1","ANTHROPIC_DEFAULT_OPUS_MODEL":"keep-opus[1M]"}}`
	_, content, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{
		Agent: "claude-code", APIKeyID: keyID, CurrentContent: &draft,
		ModelGroups: &model.ClaudeCodeModelGroups{Sonnet: 1999001},
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"DRAFT_ONLY": "1"`, `"permissions"`, `"ANTHROPIC_DEFAULT_SONNET_MODEL": "route-default"`, `"ANTHROPIC_DEFAULT_OPUS_MODEL": "keep-opus[1M]"`} {
		if !strings.Contains(content, want) {
			t.Fatalf("draft merge missing %s", want)
		}
	}
	if strings.Contains(content, "DISK_ONLY") {
		t.Fatal("editor draft must take precedence over the disk baseline")
	}
}

func TestAgentAutoConfigPrepareRejectsInvalidSelections(t *testing.T) {
	setTestGateway()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	keyID := seedAutoConfigKey(t)
	seedClaudeModelGroups(t)
	key, _ := APIKeyGet(keyID, context.Background())
	originalKey := key
	t.Cleanup(func() { apiKeyCache.Set(keyID, originalKey) })
	cases := []struct {
		name     string
		agent    string
		groups   model.ClaudeCodeModelGroups
		allowed  string
		disabled bool
	}{
		{name: "missing group", agent: "claude-code", groups: model.ClaudeCodeModelGroups{Default: 1999099}},
		{name: "negative group", agent: "claude-code", groups: model.ClaudeCodeModelGroups{Opus: -1}},
		{name: "empty selection", agent: "claude-code"},
		{name: "key model restriction", agent: "claude-code", groups: model.ClaudeCodeModelGroups{Opus: 1999002}, allowed: "route-default"},
		{name: "disabled key", agent: "claude-code", groups: model.ClaudeCodeModelGroups{Default: 1999001}, disabled: true},
		{name: "other agent", agent: "codex", groups: model.ClaudeCodeModelGroups{Default: 1999001}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configuredKey := key
			configuredKey.SupportedModels = tc.allowed
			configuredKey.Enabled = !tc.disabled
			apiKeyCache.Set(keyID, configuredKey)
			if _, _, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: tc.agent, APIKeyID: keyID, ModelGroups: &tc.groups}, context.Background()); err == nil {
				t.Fatal("invalid config selection should be rejected")
			}
		})
	}
	key.SupportedModels = " route-default , route-opus "
	apiKeyCache.Set(keyID, key)
	if _, _, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: "claude-code", APIKeyID: keyID, ModelGroups: &model.ClaudeCodeModelGroups{Opus: 1999002}}, context.Background()); err != nil {
		t.Fatalf("allowed group should be accepted: %v", err)
	}
}

func TestMergeClaudeSettingsRejectsInvalidEnv(t *testing.T) {
	for _, content := range []string{`{"env":"not an object"}`, `{"env":[]}`, `{invalid`} {
		if _, err := mergeClaudeSettings(content, "sk-test", "http://127.0.0.1:18777"); err == nil {
			t.Fatalf("invalid settings must be rejected: %s", content)
		}
	}
}

func seedClaudeModelGroups(t *testing.T) {
	t.Helper()
	for index, name := range []string{"route-default", "route-opus", "route-fast"} {
		id := 1999001 + index
		previous, existed := groupCache.Get(id)
		groupCache.Set(id, model.Group{ID: id, Name: name, Mode: model.GroupModeRoundRobin, Items: []model.GroupItem{{ChannelID: 1, ModelName: "upstream-only-model"}}})
		t.Cleanup(func() {
			if existed {
				groupCache.Set(id, previous)
			} else {
				groupCache.Del(id)
			}
		})
	}
}

func TestPatchKimiCodeTomlReplacesOwnedEntriesAndPreservesRest(t *testing.T) {
	content := `# Kimi Code 配置
default_model = "Kimi For Coding/kimi-k3.1-pro"
default_provider = "Kimi For Coding"

[providers."Kimi For Coding"]
type = "anthropic"
base_url = "https://tokenhub.example.com/plan/anthropic"
api_key = "sk-existing"

[providers.Octopus]
type = "openai"
base_url = "https://old-octopus.example.com"
api_key_env = "OLD_OCTOPUS_KEY"
oauth = { storage = "file", key = "old-oauth" }

[models."Kimi For Coding/kimi-k3.1-pro"]
provider = "Kimi For Coding"
model = "kimi-k3.1-pro"
max_context_size = 1048576

[models."Octopus/old-group"]
provider = "Octopus"
model = "old-group"
max_context_size = 8192
`
	got := patchKimiCodeToml(content, "sk-new", "http://127.0.0.1:18777", "route-default")
	for _, want := range []string{
		`# Kimi Code 配置`,
		`default_model = "Octopus/route-default"`,
		`default_provider = "Octopus"`,
		`[providers."Kimi For Coding"]`,
		`base_url = "https://tokenhub.example.com/plan/anthropic"`,
		`[models."Kimi For Coding/kimi-k3.1-pro"]`,
		`max_context_size = 1048576`,
		`[providers.Octopus]`,
		`type = "anthropic"`,
		`base_url = "http://127.0.0.1:18777"`,
		`api_key = "sk-new"`,
		`[models."Octopus/route-default"]`,
		`provider = "Octopus"`,
		`model = "route-default"`,
		`display_name = "route-default"`,
		`capabilities = ["thinking", "tool_use"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("patched Kimi Code TOML missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"OLD_OCTOPUS_KEY", "old-octopus.example.com", "old-oauth"} {
		if strings.Contains(got, gone) {
			t.Fatalf("patched Kimi Code TOML should remove %q:\n%s", gone, got)
		}
	}
	if err := validateAgentConfigFile("toml", got); err != nil {
		t.Fatalf("generated TOML is invalid: %v", err)
	}
	var parsed map[string]any
	if err := toml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["default_model"] != "Octopus/route-default" {
		t.Fatalf("default_model = %v", parsed["default_model"])
	}
	models := parsed["models"].(map[string]any)
	model := models["Octopus/route-default"].(map[string]any)
	if model["provider"] != "Octopus" || model["model"] != "route-default" || model["max_context_size"] != int64(kimiCodeDefaultMaxContext) {
		t.Fatalf("unexpected generated model: %#v", model)
	}
	again := patchKimiCodeToml(got, "sk-new", "http://127.0.0.1:18777", "route-default")
	if again != got {
		t.Fatalf("patching the same Kimi Code config twice must be idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
}

func TestPatchKimiCodeTomlEmptyAndCRLF(t *testing.T) {
	got := patchKimiCodeToml("", "sk-new", "http://127.0.0.1:18777", "route-default")
	for _, want := range []string{
		`default_provider = "Octopus"`,
		`default_model = "Octopus/route-default"`,
		`[providers.Octopus]`,
		`[models."Octopus/route-default"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("empty Kimi Code config missing %q:\n%s", want, got)
		}
	}
	if err := validateAgentConfigFile("toml", got); err != nil {
		t.Fatalf("empty generated TOML is invalid: %v", err)
	}

	crlf := patchKimiCodeToml("default_model = \"old/model\"\r\n\r\n[providers.other]\r\ntype = \"openai\"\r\n", "sk-new", "http://127.0.0.1:18777", "route-default")
	if !strings.Contains(crlf, "\r\n") || strings.Contains(strings.ReplaceAll(crlf, "\r\n", ""), "\n") {
		t.Fatalf("Kimi Code patch must preserve CRLF:\n%q", crlf)
	}
	if err := validateAgentConfigFile("toml", crlf); err != nil {
		t.Fatalf("CRLF generated TOML is invalid: %v", err)
	}
}

func TestPatchKimiCodeTomlQuotesModelAlias(t *testing.T) {
	got := patchKimiCodeToml("", "sk-new", "http://127.0.0.1:18777", "group.with-dot")
	if !strings.Contains(got, `[models."Octopus/group.with-dot"]`) {
		t.Fatalf("model alias with a dot must stay a single quoted table key:\n%s", got)
	}
	if err := validateAgentConfigFile("toml", got); err != nil {
		t.Fatalf("quoted model alias generated invalid TOML: %v", err)
	}
}

func TestAgentAutoConfigPrepareKimiCodeModelGroup(t *testing.T) {
	setTestGateway()
	t.Setenv("KIMI_CODE_HOME", t.TempDir())
	keyID := seedAutoConfigKey(t)
	seedClaudeModelGroups(t)
	original := "# keep comment\r\ndefault_model = \"old/model\"\r\n\r\n[providers.old]\r\ntype = \"openai\"\r\nbase_url = \"https://old.example.com\"\r\napi_key = \"sk-old\"\r\n"
	path := filepath.Join(os.Getenv("KIMI_CODE_HOME"), "config.toml")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	fileKey, content, err := AgentAutoConfigPrepare(model.AgentAutoConfigRequest{
		Agent: "kimi-code", APIKeyID: keyID, KimiCodeModelGroupID: 1999001,
	}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if fileKey != "config" || !strings.Contains(content, `default_model = "Octopus/route-default"`) {
		t.Fatalf("expected the selected group in Kimi Code config: %s", content)
	}
	if !strings.Contains(content, "\r\n") || !strings.Contains(content, `# keep comment`) {
		t.Fatal("Kimi Code formatting and comments must be preserved")
	}
	disk, _ := os.ReadFile(path)
	if string(disk) != original {
		t.Fatal("preparing Kimi Code configuration must not write the file")
	}
	views, err := AgentConfigList("kimi-code")
	if err != nil || len(views) != 1 || views[0].Key != "config" || views[0].Path != path {
		t.Fatalf("Kimi Code config descriptor not resolved: views=%#v err=%v", views, err)
	}
	if err := AgentConfigSave("kimi-code", []AgentConfigSaveInput{{Key: fileKey, Content: content, BaseModifiedAt: views[0].ModifiedAt}}); err != nil {
		t.Fatalf("save generated Kimi Code config: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || string(saved) != content {
		t.Fatalf("saved Kimi Code config mismatch: %v", err)
	}
	for _, req := range []model.AgentAutoConfigRequest{
		{Agent: "kimi-code", APIKeyID: keyID},
		{Agent: "kimi-code", APIKeyID: keyID, KimiCodeModelGroupID: -1},
		{Agent: "kimi-code", APIKeyID: keyID, KimiCodeModelGroupID: 1999099},
		{Agent: "codex", APIKeyID: keyID, KimiCodeModelGroupID: 1999001},
	} {
		if _, _, err := AgentAutoConfigPrepare(req, context.Background()); err == nil {
			t.Fatal("invalid Kimi Code model selection must be rejected")
		}
	}
}

func TestMergeClaudeSettingsEmptyContent(t *testing.T) {
	got, err := mergeClaudeSettings("", "sk-new", "http://127.0.0.1:18777")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(got, `"ANTHROPIC_BASE_URL"`) || !strings.Contains(got, `"ANTHROPIC_AUTH_TOKEN"`) {
		t.Fatalf("unexpected merge output:\n%s", got)
	}
}

func TestAgentAutoConfigSupported(t *testing.T) {
	if !AgentAutoConfigSupported("codex") || !AgentAutoConfigSupported("claude-code") || !AgentAutoConfigSupported("kimi-code") {
		t.Fatalf("codex, claude-code and kimi-code should support auto config")
	}
	if AgentAutoConfigSupported("zcode") || AgentAutoConfigSupported("workbuddy") {
		t.Fatalf("zcode/workbuddy should not support auto config yet")
	}
}

func TestAgentAutoConfigApplyCodex(t *testing.T) {
	setTestGateway()
	dir := setTestCodexHome(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"gpt-6.1\"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	keyID := seedAutoConfigKey(t)

	fileKey, content, err := AgentAutoConfigApply("codex", keyID, context.Background())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if fileKey != "config" {
		t.Fatalf("unexpected file key %q", fileKey)
	}
	if !strings.Contains(content, `experimental_bearer_token = "sk-auto-test"`) {
		t.Fatalf("content missing bearer token:\n%s", content)
	}
	if !strings.Contains(content, `base_url = "http://127.0.0.1:18777/v1"`) {
		t.Fatalf("content missing gateway base_url:\n%s", content)
	}
}

func TestAgentAutoConfigApplyClaude(t *testing.T) {
	setTestGateway()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	keyID := seedAutoConfigKey(t)

	fileKey, content, err := AgentAutoConfigApply("claude-code", keyID, context.Background())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if fileKey != "settings" {
		t.Fatalf("unexpected file key %q", fileKey)
	}
	if !strings.Contains(content, `"ANTHROPIC_AUTH_TOKEN": "sk-auto-test"`) {
		t.Fatalf("content missing auth token:\n%s", content)
	}
}

// autoConfigTestKeyID 测试专用密钥 id：取大值避免与拓扑测试的 id=1 查找串扰
// （密钥缓存是包级状态，跨测试存活）
const autoConfigTestKeyID = 987654

// seedAutoConfigKey 初始化测试 DB 并创建一把测试密钥，返回其 id
func seedAutoConfigKey(t *testing.T) int {
	t.Helper()
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "test.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := db.GetDB().AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatalf("migrate api_keys: %v", err)
	}
	t.Cleanup(func() {
		// Windows 下 sqlite 文件被 gorm 连接占用，需先关闭连接才能让 TempDir 清理成功
		if err := db.Close(); err != nil {
			t.Logf("close db: %v", err)
		}
	})
	key := model.APIKey{ID: autoConfigTestKeyID, Name: "auto-test", APIKey: "sk-auto-test", Enabled: true}
	if err := APIKeyCreate(&key, context.Background()); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return key.ID
}
