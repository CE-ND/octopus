package model

// AgentConfigSaveFile 保存单个配置文件的内容
type AgentConfigSaveFile struct {
	Key            string `json:"key"`
	Content        string `json:"content"`
	BaseModifiedAt int64  `json:"base_modified_at"` // 加载时的 mtime（unix 毫秒），用于外部修改冲突检测
}

// AgentConfigSaveRequest 保存 agent 配置文件的请求
type AgentConfigSaveRequest struct {
	Agent string                `json:"agent"`
	Files []AgentConfigSaveFile `json:"files"`
	DSH   *DshConfigSaveOptions `json:"dsh,omitempty"`
}

// DshRouteSelection 表示一个 Octopus 模型分组在 DSH 中使用的客户端协议。
// DSH 的 llm-pi-ai 里 api 是 provider 级别的，因此同一协议的多个分组会合并到
// 同一个 Octopus provider；不同协议必须使用不同的 provider。
type DshRouteSelection struct {
	ModelGroupID int    `json:"model_group_id"`
	API          string `json:"api"`
}

// DshRouteState 是返回给 UI 的当前 DSH 路由摘要。
type DshRouteState struct {
	Provider     string `json:"provider"`
	API          string `json:"api"`
	ModelName    string `json:"model_name"`
	ModelGroupID int    `json:"model_group_id,omitempty"`
}

// DshAutoConfigOptions 选择桌面 profile 的模型路由、默认分组及推理等级。
// Routes 为空时保留旧的单分组字段兼容路径。
type DshAutoConfigOptions struct {
	Profile             string              `json:"profile,omitempty"`
	ModelGroupID        int                 `json:"model_group_id,omitempty"`
	API                 string              `json:"api,omitempty"`
	Routes              []DshRouteSelection `json:"routes,omitempty"`
	DefaultModelGroupID int                 `json:"default_model_group_id,omitempty"`
	ReasoningEffort     *string             `json:"reasoning_effort,omitempty"` // 未传时保留兼容的原值，default 跟随提供商默认值。
}

// DshConfigSaveOptions 凭据只由后端从密钥 ID 获取，不经过文件编辑器。
type DshConfigSaveOptions struct {
	Profile             string              `json:"profile,omitempty"`
	APIKeyID            int                 `json:"api_key_id,omitempty"`
	ModelGroupID        int                 `json:"model_group_id,omitempty"`
	Routes              []DshRouteSelection `json:"routes,omitempty"`
	DefaultModelGroupID int                 `json:"default_model_group_id,omitempty"`
	CredentialRevision  string              `json:"credential_revision,omitempty"`
}

// DshConfigState 对 UI 安全的配置摘要，不包含凭据值或账号记录。
type DshConfigState struct {
	Profile              string          `json:"profile"`
	API                  string          `json:"api"`
	APIKeyID             int             `json:"api_key_id"`
	CredentialConfigured bool            `json:"credential_configured"`
	CredentialReference  string          `json:"credential_reference"`
	CredentialRevision   string          `json:"credential_revision"`
	CurrentProvider      string          `json:"current_provider"`
	CurrentModel         string          `json:"current_model"`
	DefaultModelGroupID  int             `json:"default_model_group_id,omitempty"`
	Routes               []DshRouteState `json:"routes,omitempty"`
	ReasoningEffort      string          `json:"reasoning_effort"`
}

// DshPreparedConfig 回填草稿所需的公开信息；修订仅针对本功能管理的引用。
type DshPreparedConfig struct {
	Profile             string          `json:"profile"`
	CredentialReference string          `json:"credential_reference"`
	CredentialRevision  string          `json:"credential_revision"`
	ModelGroupID        int             `json:"model_group_id"`
	ModelName           string          `json:"model_name"`
	API                 string          `json:"api"`
	DefaultModelGroupID int             `json:"default_model_group_id,omitempty"`
	Routes              []DshRouteState `json:"routes,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort"`
	ReasoningReset      bool            `json:"reasoning_reset"`
}

// ClaudeCodeModelGroups 按用途选择模型分组。0 表示保留该用途的现有模型配置。
// 客户端传分组 ID，实际写入的模型名由服务端从分组解析。
type ClaudeCodeModelGroups struct {
	Default  int `json:"default,omitempty"`
	Opus     int `json:"opus,omitempty"`
	Sonnet   int `json:"sonnet,omitempty"`
	Haiku    int `json:"haiku,omitempty"`
	Fable    int `json:"fable,omitempty"`
	Subagent int `json:"subagent,omitempty"`
}

// AgentAutoConfigRequest 把网关、密钥与选定的模型分组合并进 agent 配置。
type AgentAutoConfigRequest struct {
	Agent                string                 `json:"agent"`
	APIKeyID             int                    `json:"api_key_id"`
	ModelGroups          *ClaudeCodeModelGroups `json:"model_groups,omitempty"`
	CodexModelGroupID    int                    `json:"codex_model_group_id,omitempty"`     // 0 表示保留 Codex 当前模型
	KimiCodeModelGroupID int                    `json:"kimi_code_model_group_id,omitempty"` // Kimi Code 自动配置必填
	GeminiModelGroupID   int                    `json:"gemini_model_group_id,omitempty"`    // 0 表示保留 Gemini CLI 当前模型
	CurrentContent       *string                `json:"current_content,omitempty"`          // 单文件 agent 的编辑器草稿；省略时读取磁盘
	CurrentContents      map[string]string      `json:"current_contents,omitempty"`         // 多文件 agent 的编辑器草稿，按文件 key 索引
	DSH                  *DshAutoConfigOptions  `json:"dsh,omitempty"`
}

// AgentAutoConfigFile 自动配置写入的单个文件内容。
type AgentAutoConfigFile struct {
	FileKey string `json:"file_key"`
	Content string `json:"content"`
}

// AgentAutoConfigResponse 自动配置结果：补丁后的配置内容（由前端填入编辑器草稿）。
// FileKey/Content 保留给单文件 agent；Files 用于 Gemini CLI 这类同时写 .env 和
// settings.json 的 agent，旧客户端仍可按主文件 key 使用前两个字段。
type AgentAutoConfigResponse struct {
	FileKey string                `json:"file_key"`
	Content string                `json:"content"`
	Files   []AgentAutoConfigFile `json:"files,omitempty"`
	DSH     *DshPreparedConfig    `json:"dsh,omitempty"`
}
