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

// DshAutoConfigOptions 选择桌面 profile 的默认分组及客户端协议。
type DshAutoConfigOptions struct {
	Profile      string `json:"profile,omitempty"`
	ModelGroupID int    `json:"model_group_id"`
	API          string `json:"api,omitempty"`
}

// DshConfigSaveOptions 凭据只由后端从密钥 ID 获取，不经过文件编辑器。
type DshConfigSaveOptions struct {
	Profile            string `json:"profile,omitempty"`
	APIKeyID           int    `json:"api_key_id,omitempty"`
	ModelGroupID       int    `json:"model_group_id,omitempty"`
	CredentialRevision string `json:"credential_revision,omitempty"`
}

// DshConfigState 对 UI 安全的配置摘要，不包含凭据值或账号记录。
type DshConfigState struct {
	Profile              string `json:"profile"`
	API                  string `json:"api"`
	APIKeyID             int    `json:"api_key_id"`
	CredentialConfigured bool   `json:"credential_configured"`
	CredentialReference  string `json:"credential_reference"`
	CredentialRevision   string `json:"credential_revision"`
	CurrentProvider      string `json:"current_provider"`
	CurrentModel         string `json:"current_model"`
}

// DshPreparedConfig 回填草稿所需的公开信息；修订仅针对本功能管理的引用。
type DshPreparedConfig struct {
	Profile             string `json:"profile"`
	CredentialReference string `json:"credential_reference"`
	CredentialRevision  string `json:"credential_revision"`
	ModelGroupID        int    `json:"model_group_id"`
	ModelName           string `json:"model_name"`
	API                 string `json:"api"`
	ReasoningReset      bool   `json:"reasoning_reset"`
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
	CurrentContent       *string                `json:"current_content,omitempty"`          // 编辑器草稿；省略时读取磁盘
	DSH                  *DshAutoConfigOptions  `json:"dsh,omitempty"`
}

// AgentAutoConfigResponse 自动配置结果：补丁后的配置内容（由前端填入编辑器草稿）
type AgentAutoConfigResponse struct {
	FileKey string             `json:"file_key"`
	Content string             `json:"content"`
	DSH     *DshPreparedConfig `json:"dsh,omitempty"`
}
