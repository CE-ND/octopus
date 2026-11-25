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
//   - gemini-cli:  ~/.gemini/.env 的网关地址/密钥/模型，以及 settings.json 的
//     security.auth.selectedType 和 model.name；其余行与 JSON 字段逐字保留

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

// AgentAutoConfigPrepareFiles 与 AgentAutoConfigPrepare 相同，但额外返回需要一起
// 填入多文件 agent 草稿的所有文件（目前为 Gemini CLI 的 .env 和 settings.json）。
func AgentAutoConfigPrepareFiles(req model.AgentAutoConfigRequest, ctx context.Context) (string, string, []model.AgentAutoConfigFile, error) {
	if req.Agent == "gemini-cli" {
		return prepareGeminiCLI(req, ctx)
	}
	fileKey, content, err := AgentAutoConfigPrepare(req, ctx)
	if err != nil {
		return "", "", nil, err
	}
	return fileKey, content, nil, nil
}

// AgentAutoConfigPrepare 合并编辑器草稿、连接设置和模型选择；仍由统一保存管线写盘。
func AgentAutoConfigPrepare(req model.AgentAutoConfigRequest, ctx context.Context) (string, string, error) {
	if req.DSH != nil && req.Agent != dshAgentID {
		return "", "", fmt.Errorf("DSH options require DeepSeek Harness")
	}
	// Gemini CLI 同时拥有 .env 和 settings.json，交给专用实现生成多文件结果。
	if req.Agent == "gemini-cli" {
		fileKey, content, _, err := prepareGeminiCLI(req, ctx)
		return fileKey, content, err
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
	if req.KimiCodeModelGroupID < 0 || (req.KimiCodeModelGroupID > 0 && agent != "kimi-code") {
		return "", "", fmt.Errorf("invalid Kimi Code model group selection")
	}
	if req.GeminiModelGroupID < 0 || (req.GeminiModelGroupID > 0 && agent != "gemini-cli") {
		return "", "", fmt.Errorf("invalid Gemini CLI model group selection")
	}
	if req.CurrentContents != nil && agent != "gemini-cli" {
		return "", "", fmt.Errorf("multi-file config drafts are only supported for Gemini CLI")
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
	case "kimi-code":
		if err := validateAgentConfigFile("toml", current); err != nil {
			return "", "", fmt.Errorf("config.toml is not valid TOML: %w", err)
		}
		if req.KimiCodeModelGroupID <= 0 {
			return "", "", fmt.Errorf("select a model group for Kimi Code")
		}
		modelName, err := agentModelGroupName(req.KimiCodeModelGroupID, key.SupportedModels, ctx)
		if err != nil {
			return "", "", err
		}
		patched := patchKimiCodeToml(current, apiKey, base, modelName)
		if err := validateAgentConfigFile("toml", patched); err != nil {
			return "", "", fmt.Errorf("generated Kimi Code config.toml is invalid: %w", err)
		}
		return def.AutoConfigFileKey, patched, nil
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

// prepareGeminiCLI 生成 Gemini CLI 的 .env 和 settings.json 两个文件的补丁。
// .env 是真正携带密钥的文件；settings.json 只切换认证方式并可选更新 model.name，
// 因此两个文件必须一起返回给前端草稿，用户确认后由统一保存管线落盘。
func prepareGeminiCLI(req model.AgentAutoConfigRequest, ctx context.Context) (string, string, []model.AgentAutoConfigFile, error) {
	if req.DSH != nil {
		return "", "", nil, fmt.Errorf("DSH options require DeepSeek Harness")
	}
	if req.ModelGroups != nil {
		return "", "", nil, fmt.Errorf("model group selection is only supported for Claude Code")
	}
	if req.CodexModelGroupID != 0 || req.KimiCodeModelGroupID != 0 {
		return "", "", nil, fmt.Errorf("invalid model group selection")
	}
	if req.GeminiModelGroupID < 0 {
		return "", "", nil, fmt.Errorf("invalid Gemini CLI model group selection")
	}
	var def *AgentDefinition
	for i := range AgentDefinitions {
		if AgentDefinitions[i].ID == "gemini-cli" {
			def = &AgentDefinitions[i]
			break
		}
	}
	if def == nil || def.AutoConfigFileKey != "env" {
		return "", "", nil, fmt.Errorf("agent %q does not support auto config", "gemini-cli")
	}
	key, err := APIKeyGet(req.APIKeyID, ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("api key %d not found: %w", req.APIKeyID, err)
	}
	if !key.Enabled {
		return "", "", nil, fmt.Errorf("api key %d is disabled", req.APIKeyID)
	}
	apiKey := strings.TrimSpace(key.APIKey)
	if apiKey == "" {
		return "", "", nil, fmt.Errorf("api key %d has an empty value", req.APIKeyID)
	}
	selectedModel := ""
	if req.GeminiModelGroupID > 0 {
		selectedModel, err = agentModelGroupName(req.GeminiModelGroupID, key.SupportedModels, ctx)
		if err != nil {
			return "", "", nil, err
		}
	}

	envContent := ""
	settingsContent := ""
	if req.CurrentContents != nil {
		envContent = req.CurrentContents["env"]
		settingsContent = req.CurrentContents["settings"]
	} else {
		if req.CurrentContent != nil {
			envContent = *req.CurrentContent
		} else {
			envContent, err = readAgentConfigContent(def, "env")
			if err != nil {
				return "", "", nil, err
			}
		}
		settingsContent, err = readAgentConfigContent(def, "settings")
		if err != nil {
			return "", "", nil, err
		}
	}

	patchedEnv := patchGeminiEnv(envContent, apiKey, gatewayBaseURL(), selectedModel)
	patchedSettings, err := mergeGeminiSettings(settingsContent, selectedModel)
	if err != nil {
		return "", "", nil, err
	}
	files := []model.AgentAutoConfigFile{
		{FileKey: "env", Content: patchedEnv},
		{FileKey: "settings", Content: patchedSettings},
	}
	return "env", patchedEnv, files, nil
}

func readAgentConfigContent(def *AgentDefinition, key string) (string, error) {
	if def == nil || def.ConfigFiles == nil {
		return "", nil
	}
	for _, file := range def.ConfigFiles() {
		if file.Key != key {
			continue
		}
		data, err := os.ReadFile(file.Path)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read agent config: %w", err)
		}
		return string(data), nil
	}
	return "", nil
}

// geminiAutoConfigEnvKey 判断一个环境变量是否属于 Gemini CLI 的连接/鉴权字段。
// GOOGLE_* 整个前缀都属于连接和鉴权；GEMINI_* 只取与鉴权相关的几个键，不能按前缀
// 删除，否则会误删 GEMINI_SANDBOX、GEMINI_SYSTEM_MD 等用户设置。
func geminiAutoConfigEnvKey(key string, hasSelectedModel bool) bool {
	if strings.HasPrefix(key, "GOOGLE_") {
		return true
	}
	if key == "GEMINI_MODEL" {
		return hasSelectedModel
	}
	switch key {
	case "GEMINI_API_KEY",
		"GEMINI_API_KEY_AUTH_MECHANISM",
		"GEMINI_CLI_CUSTOM_HEADERS",
		"GEMINI_DEFAULT_AUTH_TYPE",
		"GEMINI_CLI_USE_COMPUTE_ADC",
		"CODE_ASSIST_ENDPOINT",
		"CODE_ASSIST_API_VERSION":
		return true
	}
	return false
}

// patchGeminiEnv 只改写 Gemini CLI 拥有的环境变量，保留用户注释、空行、
// 其他变量以及原有顺序。GEMINI_MODEL 仅在用户明确选择分组时写入；未选择时
// 保留当前模型配置，避免把用户已有模型重置掉。
func patchGeminiEnv(content, apiKey, apiBaseURL, selectedModel string) string {
	desired := [][2]string{
		{"GOOGLE_GEMINI_BASE_URL", apiBaseURL},
		{"GEMINI_API_KEY", apiKey},
	}
	if selectedModel != "" {
		desired = append(desired, [2]string{"GEMINI_MODEL", selectedModel})
	}
	owned := make(map[string]string, len(desired))
	for _, pair := range desired {
		owned[pair[0]] = pair[1]
	}

	crlf := strings.Contains(content, "\r\n")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	trailingNewline := normalized == "" || strings.HasSuffix(normalized, "\n")
	if trailingNewline && normalized != "" {
		normalized = strings.TrimSuffix(normalized, "\n")
	}
	lines := []string{}
	if normalized != "" {
		lines = strings.Split(normalized, "\n")
	}
	out := make([]string, 0, len(lines)+len(desired))
	emitted := make(map[string]bool, len(desired))
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, raw)
			continue
		}
		body := trimmed
		exportPrefix := ""
		if strings.HasPrefix(body, "export ") {
			exportPrefix = "export "
			body = strings.TrimSpace(strings.TrimPrefix(body, "export "))
		}
		eq := strings.Index(body, "=")
		if eq <= 0 {
			out = append(out, raw)
			continue
		}
		key := strings.TrimSpace(body[:eq])
		value, managed := owned[key]
		if !managed {
			if geminiAutoConfigEnvKey(key, selectedModel != "") {
				// 切到 API Key 模式时清掉 Google 登录 / Vertex / 自定义鉴权等
				// 会覆盖本配置的连接变量；普通 GEMINI_* 设置原样保留。
				continue
			}
			out = append(out, raw)
			continue
		}
		if !emitted[key] {
			// 原位改写第一处定义，保留用户的 export 前缀；重复定义直接删除。
			out = append(out, exportPrefix+key+"="+value)
			emitted[key] = true
		}
	}
	for _, pair := range desired {
		if !emitted[pair[0]] {
			out = append(out, pair[0]+"="+pair[1])
		}
	}
	result := strings.Join(out, "\n")
	if trailingNewline && result != "" {
		result += "\n"
	}
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return result
}

func geminiJSONObject(parent map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	raw, ok := parent[key]
	if !ok || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return map[string]json.RawMessage{}, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%s must be an object: %w", key, err)
	}
	if object == nil {
		object = map[string]json.RawMessage{}
	}
	return object, nil
}

func setGeminiJSONString(parent map[string]json.RawMessage, key, value string) {
	encoded, _ := json.Marshal(value)
	parent[key] = encoded
}

func setGeminiJSONObject(parent map[string]json.RawMessage, key string, object map[string]json.RawMessage) {
	encoded, _ := json.Marshal(object)
	parent[key] = encoded
}

// mergeGeminiSettings 只更新 Gemini CLI 的认证方式和可选默认模型，保留其他字段。
func mergeGeminiSettings(content, selectedModel string) (string, error) {
	root := map[string]json.RawMessage{}
	if strings.TrimSpace(content) != "" {
		if err := json.Unmarshal([]byte(content), &root); err != nil {
			return "", fmt.Errorf("settings.json is not valid JSON: %w", err)
		}
	}
	if root == nil {
		root = map[string]json.RawMessage{}
	}
	security, err := geminiJSONObject(root, "security")
	if err != nil {
		return "", err
	}
	auth, err := geminiJSONObject(security, "auth")
	if err != nil {
		return "", err
	}
	setGeminiJSONString(auth, "selectedType", "gemini-api-key")
	setGeminiJSONObject(security, "auth", auth)
	setGeminiJSONObject(root, "security", security)
	if selectedModel != "" {
		model, err := geminiJSONObject(root, "model")
		if err != nil {
			return "", err
		}
		setGeminiJSONString(model, "name", selectedModel)
		setGeminiJSONObject(root, "model", model)
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
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

// kimiCodeProviderID 是自动配置写入 Kimi Code 的供应商标识。
// Kimi Code 的 config.toml 用 [providers.<id>] 与 [models."<provider>/<alias>"] 组织模型，
// default_model 必须引用 models 表中的完整 key；Anthropic 类型会自动拼接 /v1/messages。
const kimiCodeProviderID = "Octopus"

// kimiCodeDefaultMaxContext 是自动配置生成模型的保守上下文长度，
// 与 Kimi Code 导入未知模型时的默认值保持一致。
const kimiCodeDefaultMaxContext = 131072

type tomlDocumentSection struct {
	header string
	path   []string
	lines  []string
}

// patchKimiCodeToml 只改写自动配置拥有的键，保留用户已有供应商、模型和注释。
func patchKimiCodeToml(content, apiKey, apiBaseURL, modelGroup string) string {
	crlf := strings.Contains(content, "\r\n")
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	root, sections := splitTOMLDocument(normalized)

	alias := kimiCodeProviderID + "/" + modelGroup
	providerPath := []string{"providers", kimiCodeProviderID}
	modelPath := []string{"models", alias}
	providerFound := false
	modelFound := false

	for i := range sections {
		switch {
		case sameTOMLPath(sections[i].path, providerPath):
			sections[i].lines = patchTOMLSection(sections[i].lines, [][2]string{
				{"type", strconv.Quote("anthropic")},
				{"base_url", strconv.Quote(apiBaseURL)},
				{"api_key", strconv.Quote(apiKey)},
			}, []string{"api_key_env", "oauth"})
			providerFound = true
		case sameTOMLPath(sections[i].path, modelPath):
			sections[i].lines = patchTOMLSection(sections[i].lines, [][2]string{
				{"provider", strconv.Quote(kimiCodeProviderID)},
				{"model", strconv.Quote(modelGroup)},
				{"max_context_size", strconv.Itoa(kimiCodeDefaultMaxContext)},
				{"display_name", strconv.Quote(modelGroup)},
				{"capabilities", `["thinking", "tool_use"]`},
			}, nil)
			modelFound = true
		}
	}

	root = patchTOMLSection(root, [][2]string{
		{"default_provider", strconv.Quote(kimiCodeProviderID)},
		{"default_model", strconv.Quote(alias)},
	}, nil)

	out := renderTOMLDocument(root, sections)
	if !providerFound {
		appendTOMLSection(&out, "[providers."+kimiCodeProviderID+"]", []string{
			`type = "anthropic"`,
			"base_url = " + strconv.Quote(apiBaseURL),
			"api_key = " + strconv.Quote(apiKey),
		})
	}
	if !modelFound {
		appendTOMLSection(&out, "[models."+strconv.Quote(alias)+"]", []string{
			`provider = "Octopus"`,
			"model = " + strconv.Quote(modelGroup),
			"max_context_size = " + strconv.Itoa(kimiCodeDefaultMaxContext),
			"display_name = " + strconv.Quote(modelGroup),
			`capabilities = ["thinking", "tool_use"]`,
		})
	}

	result := strings.Join(out, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	if crlf {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return result
}

func splitTOMLDocument(content string) ([]string, []tomlDocumentSection) {
	lines := strings.Split(content, "\n")
	root := make([]string, 0, len(lines))
	sections := make([]tomlDocumentSection, 0)
	for _, line := range lines {
		if path, ok := parseTOMLTableHeader(line); ok {
			sections = append(sections, tomlDocumentSection{header: line, path: path})
			continue
		}
		if len(sections) == 0 {
			root = append(root, line)
		} else {
			last := &sections[len(sections)-1]
			last.lines = append(last.lines, line)
		}
	}
	if len(root) == 1 && root[0] == "" {
		root = nil
	}
	return root, sections
}

func renderTOMLDocument(root []string, sections []tomlDocumentSection) []string {
	out := append([]string{}, root...)
	for _, section := range sections {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, section.header)
		out = append(out, section.lines...)
	}
	return out
}

func appendTOMLSection(lines *[]string, header string, body []string) {
	if len(*lines) > 0 && strings.TrimSpace((*lines)[len(*lines)-1]) != "" {
		*lines = append(*lines, "")
	}
	*lines = append(*lines, header)
	*lines = append(*lines, body...)
}

func patchTOMLSection(lines []string, owned [][2]string, removed []string) []string {
	for _, key := range removed {
		lines, _ = replaceTOMLKey(lines, key, "", true)
	}
	for _, pair := range owned {
		var found bool
		lines, found = replaceTOMLKey(lines, pair[0], pair[1], false)
		if !found {
			lines = appendTOMLKey(lines, pair[0], pair[1])
		}
	}
	return lines
}

// replaceTOMLKey 替换或删除一个键，并一并消费多行数组/内联表值。
func replaceTOMLKey(lines []string, key, value string, remove bool) ([]string, bool) {
	out := make([]string, 0, len(lines))
	found := false
	for i := 0; i < len(lines); {
		parsedKey, valueText, ok := parseTOMLAssignment(lines[i])
		if !ok || parsedKey != key {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i + 1
		for !tomlValueComplete(valueText) && j < len(lines) {
			valueText += "\n" + lines[j]
			j++
		}
		found = true
		if !remove {
			out = append(out, key+" = "+value)
		}
		i = j
	}
	return out, found
}

func appendTOMLKey(lines []string, key, value string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	out := append([]string{}, lines[:end]...)
	out = append(out, key+" = "+value)
	out = append(out, lines[end:]...)
	return out
}

func parseTOMLAssignment(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return "", "", false
	}
	eq := strings.Index(trimmed, "=")
	if eq <= 0 {
		return "", "", false
	}
	rawKey := strings.TrimSpace(trimmed[:eq])
	if rawKey == "" {
		return "", "", false
	}
	return unquoteTOMLKey(rawKey), trimmed[eq+1:], true
}

func parseTOMLTableHeader(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' || strings.HasPrefix(trimmed, "[[") {
		return nil, false
	}
	end := -1
	var quote rune
	escaped := false
	for i, r := range trimmed {
		if i == 0 {
			continue
		}
		if quote != 0 {
			if quote == '"' {
				if escaped {
					escaped = false
					continue
				}
				if r == '\\' {
					escaped = true
					continue
				}
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if r == ']' {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, false
	}
	rest := strings.TrimSpace(trimmed[end+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return nil, false
	}
	path, err := splitTOMLKeyPath(trimmed[1:end])
	if err != nil {
		return nil, false
	}
	return path, true
}

func splitTOMLKeyPath(path string) ([]string, error) {
	var parts []string
	var quote rune
	escaped := false
	start := 0
	for i, r := range path {
		if quote != 0 {
			if quote == '"' {
				if escaped {
					escaped = false
					continue
				}
				if r == '\\' {
					escaped = true
					continue
				}
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if r == '.' {
			part := strings.TrimSpace(path[start:i])
			if part == "" {
				return nil, fmt.Errorf("empty TOML key segment")
			}
			parts = append(parts, unquoteTOMLKey(part))
			start = i + 1
		}
	}
	if start > len(path) {
		return nil, fmt.Errorf("invalid TOML key path")
	}
	last := strings.TrimSpace(path[start:])
	if last == "" {
		return nil, fmt.Errorf("empty TOML key segment")
	}
	parts = append(parts, unquoteTOMLKey(last))
	return parts, nil
}

func unquoteTOMLKey(raw string) string {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		if value, err := strconv.Unquote(raw); err == nil {
			return value
		}
	}
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1]
	}
	return raw
}

func sameTOMLPath(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func tomlValueComplete(value string) bool {
	var quote rune
	brackets := 0
	braces := 0
	escaped := false
	for _, r := range value {
		if quote != 0 {
			if quote == '"' && escaped {
				escaped = false
				continue
			}
			if quote == '"' && r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '"', '\'':
			quote = r
		case '[':
			brackets++
		case ']':
			brackets--
		case '{':
			braces++
		case '}':
			braces--
		case '#':
			return brackets == 0 && braces == 0 && quote == 0
		}
	}
	return quote == 0 && brackets == 0 && braces == 0
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
