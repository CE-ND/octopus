package op

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
)

// 「自动配置章鱼环境」：把 Octopus 网关地址和指定 API Key 合并进 agent 的配置文件。
// 借鉴 cc-switch 的「字段所有权」思想——只改写本功能拥有的键，其余内容（注释、
// 用户自建表格、键顺序）逐字保留：
//   - codex:       config.toml 顶层 model_provider / experimental_bearer_token +
//     [model_providers.custom] 表内 name / base_url / wire_api / requires_openai_auth
//   - claude-code: 网关和密钥，以及用户明确选择的默认模型/用途模型字段

const autoConfigCodexTable = "[model_providers.custom]"

// AgentAutoConfigFileKey 返回 agent 自动配置写入的配置文件 key，空串表示不支持
func AgentAutoConfigFileKey(agent string) string {
	for i := range AgentDefinitions {
		if AgentDefinitions[i].ID == agent {
			return AgentDefinitions[i].AutoConfigFileKey
		}
	}
	return ""
}

// AgentAutoConfigSupported agent 是否支持自动配置章鱼环境
func AgentAutoConfigSupported(agent string) bool {
	return AgentAutoConfigFileKey(agent) != ""
}

// gatewayBaseURL 网关根地址（不带 /v1）
func gatewayBaseURL() string {
	host := strings.TrimSpace(conf.AppConfig.Server.Host)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, conf.AppConfig.Server.Port)
}

// AgentAutoConfigApply 生成「自动配置章鱼环境」补丁后的配置内容。
// 只生成不写盘：内容由前端填入编辑器草稿，走统一的保存管线（校验/备份/冲突检测）。
// 返回 (写入的配置文件 key, 补丁后内容)。
func AgentAutoConfigApply(agent string, apiKeyID int, ctx context.Context) (string, string, error) {
	return AgentAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: agent, APIKeyID: apiKeyID}, ctx)
}

// AgentAutoConfigPrepare 合并编辑器草稿、连接设置和模型选择；仍由统一保存管线写盘。
func AgentAutoConfigPrepare(req model.AgentAutoConfigRequest, ctx context.Context) (string, string, error) {
	if req.DSH != nil && req.Agent != dshAgentID {
		return "", "", fmt.Errorf("DSH options require DeepSeek Harness")
	}
	agent := req.Agent
	var def *AgentDefinition
	for i := range AgentDefinitions {
		if AgentDefinitions[i].ID == agent {
			def = &AgentDefinitions[i]
			break
		}
	}
	if def == nil || def.AutoConfigFileKey == "" {
		return "", "", fmt.Errorf("agent %q does not support auto config", agent)
	}
	if req.ModelGroups != nil && agent != "claude-code" {
		return "", "", fmt.Errorf("model group selection is only supported for Claude Code")
	}
	if req.CodexModelGroupID < 0 || (req.CodexModelGroupID > 0 && agent != "codex") {
		return "", "", fmt.Errorf("invalid Codex model group selection")
	}
	key, err := APIKeyGet(req.APIKeyID, ctx)
	if err != nil {
		return "", "", fmt.Errorf("api key %d not found: %w", req.APIKeyID, err)
	}
	if !key.Enabled {
		return "", "", fmt.Errorf("api key %d is disabled", req.APIKeyID)
	}
	apiKey := strings.TrimSpace(key.APIKey)
	if apiKey == "" {
		return "", "", fmt.Errorf("api key %d has an empty value", req.APIKeyID)
	}
	models, err := resolveClaudeModelGroups(req.ModelGroups, key.SupportedModels, ctx)
	if err != nil {
		return "", "", err
	}

	current := ""
	if req.CurrentContent != nil {
		current = *req.CurrentContent
	} else if def.ConfigFiles != nil {
		for _, f := range def.ConfigFiles() {
			if f.Key != def.AutoConfigFileKey {
				continue
			}
			if data, err := os.ReadFile(f.Path); err == nil {
				current = string(data)
			} else if !os.IsNotExist(err) {
				return "", "", fmt.Errorf("read agent config: %w", err)
			}
		}
	}

	base := gatewayBaseURL()
	switch agent {
	case "codex":
		if err := validateAgentConfigFile("toml", current); err != nil {
			return "", "", fmt.Errorf("config.toml is not valid TOML: %w", err)
		}
		selectedModel := ""
		if req.CodexModelGroupID > 0 {
			selectedModel, err = agentModelGroupName(req.CodexModelGroupID, key.SupportedModels, ctx)
			if err != nil {
				return "", "", err
			}
		}
		return def.AutoConfigFileKey, patchCodexTomlModel(current, apiKey, base+"/v1", selectedModel), nil
	case "claude-code":
		merged, err := mergeClaudeSettingsModels(current, apiKey, base, models)
		if err != nil {
			return "", "", err
		}
		return def.AutoConfigFileKey, merged, nil
	}
	return "", "", fmt.Errorf("agent %q does not support auto config", agent)
}

// agentModelGroupName 从分组解析对外模型名，并验证该密钥是否允许访问。
func agentModelGroupName(id int, supportedModels string, ctx context.Context) (string, error) {
	group, err := GroupGet(id, ctx)
	if err != nil || strings.TrimSpace(group.Name) == "" {
		return "", fmt.Errorf("model group %d not found", id)
	}
	if strings.TrimSpace(supportedModels) != "" {
		for _, allowed := range strings.Split(supportedModels, ",") {
			if strings.TrimSpace(allowed) == group.Name {
				return group.Name, nil
			}
		}
		return "", fmt.Errorf("API key does not allow model group %q", group.Name)
	}
	return group.Name, nil
}

type claudeModelSelection struct {
	Default  string
	Opus     string
	Sonnet   string
	Haiku    string
	Fable    string
	Subagent string
}

// resolveClaudeModelGroups 使用分组对外暴露的别名，并检查所选密钥的模型权限。
// 不直接写渠道中的上游模型名，避免绕过分组路由或生成无法访问的配置。
func resolveClaudeModelGroups(groups *model.ClaudeCodeModelGroups, supportedModels string, ctx context.Context) (claudeModelSelection, error) {
	selection := claudeModelSelection{}
	if groups == nil {
		return selection, nil
	}
	allowed := map[string]bool{}
	if strings.TrimSpace(supportedModels) != "" {
		for _, name := range strings.Split(supportedModels, ",") {
			allowed[strings.TrimSpace(name)] = true
		}
	}
	fields := []struct {
		role  string
		id    int
		value *string
	}{
		{"default", groups.Default, &selection.Default},
		{"opus", groups.Opus, &selection.Opus},
		{"sonnet", groups.Sonnet, &selection.Sonnet},
		{"haiku", groups.Haiku, &selection.Haiku},
		{"fable", groups.Fable, &selection.Fable},
		{"subagent", groups.Subagent, &selection.Subagent},
	}
	selected := false
	for _, field := range fields {
		if field.id < 0 {
			return selection, fmt.Errorf("invalid %s model group", field.role)
		}
		if field.id == 0 {
			continue
		}
		group, err := GroupGet(field.id, ctx)
		if err != nil || strings.TrimSpace(group.Name) == "" {
			return selection, fmt.Errorf("%s model group %d not found", field.role, field.id)
		}
		if len(allowed) > 0 && !allowed[group.Name] {
			return selection, fmt.Errorf("API key does not allow model group %q", group.Name)
		}
		*field.value = group.Name
		selected = true
	}
	if !selected {
		return selection, fmt.Errorf("select at least one model group")
	}
	return selection, nil
}

// patchCodexToml 行级补丁：只改写本功能拥有的键，其余行逐字保留。
// 顶层键缺失时插到第一个表格之前（TOML 顶层键必须在表格前），
// 目标表格缺失时在文末追加，表格内缺失键在表格尾部补齐。
func patchCodexToml(content, apiKey, apiBaseURL string) string {
	return patchCodexTomlModel(content, apiKey, apiBaseURL, "")
}

// patchCodexTomlModel 在连接补丁之外，仅在用户选定分组时改写顶层 model。
func patchCodexTomlModel(content, apiKey, apiBaseURL, selectedModel string) string {
	crlf := strings.Contains(content, "\r\n")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")

	topOwned := [][2]string{
		{"model_provider", `"custom"`},
		// 注意：experimental_bearer_token 只写在 [model_providers.custom] 表内。
		// 顶层写 bearer 不是 codex 的标准配置键，Codex 桌面端重写配置时会出现
		// 解析异常导致密钥不被附带（实测 401 api_key_missing），cc-switch 也不写顶层。
	}
	if selectedModel != "" {
		topOwned = append(topOwned, [2]string{"model", strconv.Quote(selectedModel)})
	}
	tableOwned := [][2]string{
		{"name", `"octopus"`},
		{"base_url", strconv.Quote(apiBaseURL)},
		{"wire_api", `"responses"`},
		{"requires_openai_auth", "false"},
		{"experimental_bearer_token", strconv.Quote(apiKey)},
	}
	topValues := map[string]string{}
	for _, kv := range topOwned {
		topValues[kv[0]] = kv[1]
	}
	tableValues := map[string]string{}
	for _, kv := range tableOwned {
		tableValues[kv[0]] = kv[1]
	}

	lines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(lines)+8)
	seenTop := map[string]bool{}
	seenTable := map[string]bool{}
	inTarget := false
	inTop := true
	targetSeen := false
	topInjected := false

	// 在第一个表格之前补齐缺失的顶层键（整个文件无表格时在末尾补）
	injectTop := func() {
		if topInjected {
			return
		}
		topInjected = true
		for _, kv := range topOwned {
			if !seenTop[kv[0]] {
				out = append(out, kv[0]+" = "+kv[1])
			}
		}
	}
	// 离开目标表格时补齐表格内缺失的键
	injectTableKeys := func() {
		for _, kv := range tableOwned {
			if !seenTable[kv[0]] {
				out = append(out, kv[0]+" = "+kv[1])
			}
		}
	}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && strings.HasPrefix(trimmed, "[") {
			if inTarget {
				injectTableKeys()
			}
			inTarget = false
			inTop = false
			if !topInjected {
				injectTop()
			}
			if trimmed == autoConfigCodexTable {
				targetSeen = true
				inTarget = true
			}
			out = append(out, raw)
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, raw)
			continue
		}
		eq := strings.Index(trimmed, "=")
		if eq <= 0 {
			out = append(out, raw)
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		if inTarget {
			if value, ok := tableValues[key]; ok {
				seenTable[key] = true
				out = append(out, rewriteTOMLValue(raw, value))
				continue
			}
		} else if value, ok := topValues[key]; inTop && ok {
			seenTop[key] = true
			out = append(out, rewriteTOMLValue(raw, value))
			continue
		} else if inTop && key == "experimental_bearer_token" {
			// 顶层的 bearer 是历史误写位置（密钥只应存在于表内），直接移除该行
			continue
		}
		out = append(out, raw)
	}
	if inTarget {
		injectTableKeys()
	}
	if !topInjected {
		injectTop()
	}
	if !targetSeen {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, autoConfigCodexTable)
		for _, kv := range tableOwned {
			out = append(out, kv[0]+" = "+kv[1])
		}
	}

	result := strings.Join(out, "\n")
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

// rewriteTOMLValue 保留键名部分的原始格式，仅替换等号后的值
func rewriteTOMLValue(rawLine, value string) string {
	idx := strings.Index(rawLine, "=")
	if idx < 0 {
		return rawLine
	}
	return rawLine[:idx+1] + " " + value
}

// mergeClaudeSettings 合并 settings.json：设置 env.ANTHROPIC_BASE_URL /
// env.ANTHROPIC_AUTH_TOKEN，其余字段保留；输出 2 空格缩进 JSON。
func mergeClaudeSettings(content, apiKey, apiBaseURL string) (string, error) {
	return mergeClaudeSettingsModels(content, apiKey, apiBaseURL, claudeModelSelection{})
}

// mergeClaudeSettingsModels 只更新明确选择的模型；未选的用途及其他用户字段保留。
// RawMessage 同时保留未知字段中的数值精度。
func mergeClaudeSettingsModels(content, apiKey, apiBaseURL string, models claudeModelSelection) (string, error) {
	var root map[string]json.RawMessage
	if strings.TrimSpace(content) != "" {
		if err := json.Unmarshal([]byte(content), &root); err != nil {
			return "", fmt.Errorf("settings.json is not valid JSON: %w", err)
		}
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	env := map[string]json.RawMessage{}
	if raw, exists := root["env"]; exists {
		if err := json.Unmarshal(raw, &env); err != nil {
			return "", fmt.Errorf("settings.json env must be an object: %w", err)
		}
		if env == nil {
			env = map[string]json.RawMessage{}
		}
	}
	setString := func(values map[string]json.RawMessage, key, value string) {
		encoded, _ := json.Marshal(value)
		values[key] = encoded
	}
	setString(env, "ANTHROPIC_BASE_URL", apiBaseURL)
	setString(env, "ANTHROPIC_AUTH_TOKEN", apiKey)
	if models.Default != "" {
		setString(root, "model", models.Default)
		setString(env, "ANTHROPIC_MODEL", models.Default)
	}
	for _, field := range []struct {
		role  string
		value string
	}{
		{"OPUS", models.Opus},
		{"SONNET", models.Sonnet},
		{"HAIKU", models.Haiku},
		{"FABLE", models.Fable},
	} {
		if field.value == "" {
			continue
		}
		setString(env, "ANTHROPIC_DEFAULT_"+field.role+"_MODEL", field.value)
		setString(env, "ANTHROPIC_DEFAULT_"+field.role+"_MODEL_NAME", field.value)
	}
	if models.Subagent != "" {
		setString(env, "CLAUDE_CODE_SUBAGENT_MODEL", models.Subagent)
	}
	if models.Haiku != "" {
		if _, exists := env["ANTHROPIC_SMALL_FAST_MODEL"]; exists {
			setString(env, "ANTHROPIC_SMALL_FAST_MODEL", models.Haiku)
		}
	}
	root["env"], _ = json.Marshal(env)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}
