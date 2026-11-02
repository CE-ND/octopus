package op

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 本机 agent 安装发现 + 请求归因的统一注册表：参照 magpie 的思路，
// 通过特征配置目录判断电脑上装了哪些 AI Agent（未调用的也展示为空闲节点），
// 通过 User-Agent 子串把请求归因到具体 agent。注册表按需扩展。

// homeJoin 在用户主目录下拼接相对路径，主目录不可解析时返回空串
func homeJoin(rel ...string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(append([]string{home}, rel...)...)
}

// envOrHomeDir 优先取环境变量指定的目录，否则取主目录下的默认位置
func envOrHomeDir(envKey string, rel ...string) func() []string {
	return func() []string {
		if root := strings.TrimSpace(os.Getenv(envKey)); root != "" {
			return []string{root}
		}
		dir := homeJoin(rel...)
		if dir == "" {
			return nil
		}
		return []string{dir}
	}
}

func geminiHomeDir() string {
	return homeJoin(".gemini")
}

// AgentConfigFile agent 可编辑配置文件的描述符。
// Key 是前端写入时的白名单标识，真实路径永远由注册表解析，客户端不传路径。
type AgentConfigFile struct {
	Key       string `json:"key"`
	Path      string `json:"path"`
	Language  string `json:"language"`  // toml / json / yaml
	Sensitive bool   `json:"sensitive"` // 凭据文件：0600 权限
}

// AgentDefinition 一个可识别 agent 的特征定义。
// Dirs 任一存在即视为已安装；UA 子串用于请求归因（小写匹配）；
// ConfigFiles 声明该 agent 可在拓扑页编辑的配置文件（nil 表示暂不支持编辑）；
// AutoConfigFileKey 声明「自动配置章鱼环境」写入的配置文件 key（空表示暂不支持）。
type AgentDefinition struct {
	ID                string
	Name              string
	Dirs              func() []string
	UAMatches         []string
	ConfigFiles       func() []AgentConfigFile
	AutoConfigFileKey string
}

var AgentDefinitions = []AgentDefinition{
	{
		ID:        "claude-code",
		Name:      "Claude Code",
		Dirs:      func() []string { return []string{claudeHomeDir()} },
		UAMatches: []string{"claude-cli", "claude-code"},
		ConfigFiles: func() []AgentConfigFile {
			home := claudeHomeDir()
			if home == "" {
				return nil
			}
			return []AgentConfigFile{
				{Key: "settings", Path: filepath.Join(home, "settings.json"), Language: "json", Sensitive: true},
			}
		},
		AutoConfigFileKey: "settings",
	},
	{
		ID:        "codex",
		Name:      "Codex",
		Dirs:      func() []string { return []string{codexHomeDir()} },
		UAMatches: []string{"codex_cli", "codex-cli", "codex/"},
		ConfigFiles: func() []AgentConfigFile {
			home := codexHomeDir()
			if home == "" {
				return nil
			}
			return []AgentConfigFile{
				{Key: "config", Path: filepath.Join(home, "config.toml"), Language: "toml"},
				{Key: "auth", Path: filepath.Join(home, "auth.json"), Language: "json", Sensitive: true},
			}
		},
		AutoConfigFileKey: "config",
	},
	{
		ID:                "deepseek-harness",
		Name:              "DeepSeek Harness",
		Dirs:              envOrHomeDir("DSH_HOME", ".dsh"),
		UAMatches:         []string{"deepseek-harness/"},
		ConfigFiles:       dshConfigFiles,
		AutoConfigFileKey: "profile",
	},
	{
		ID:        "zcode",
		Name:      "ZCode",
		Dirs:      envOrHomeDir("ZCODE_HOME", ".zcode"),
		UAMatches: []string{"zcode"},
	},
	{
		ID:   "workbuddy",
		Name: "WorkBuddy",
		Dirs: func() []string {
			return []string{homeJoin(".workbuddy"), homeJoin(".workbuddy-ai"), homeJoin("AppData", "Local", "workbuddy-ai")}
		},
		UAMatches: []string{"workbuddy"},
	},
	{
		ID:        "codebuddy",
		Name:      "CodeBuddy",
		Dirs:      envOrHomeDir("CODEBUDDY_HOME", ".codebuddy"),
		UAMatches: []string{"codebuddy"},
	},
	{
		ID:        "copilot",
		Name:      "Copilot",
		Dirs:      envOrHomeDir("GITHUB_COPILOT_HOME", ".copilot"),
		UAMatches: []string{"github-copilot", "copilot"},
	},
	{
		ID:   "qoder",
		Name: "Qoder",
		Dirs: func() []string {
			return []string{homeJoin(".qoder"), homeJoin(".qoder-cn")}
		},
		UAMatches: []string{"qoder"},
	},
	{
		ID:        "gemini-cli",
		Name:      "Gemini CLI",
		Dirs:      func() []string { return []string{geminiHomeDir()} },
		UAMatches: []string{"gemini-cli", "geminicli", "gemini/"},
	},
}

// InstalledAgent 安装发现结果
type InstalledAgent struct {
	Agent      string `json:"agent"`
	Name       string `json:"name"`
	Installed  bool   `json:"installed"`
	HasConfig  bool   `json:"has_config"`
	AutoConfig bool   `json:"auto_config"`
}

var (
	installedAgentsMu       sync.Mutex
	installedAgentsCache    []InstalledAgent
	installedAgentsCachedAt time.Time
)

// InstalledAgentsGet 返回本机 agent 安装情况（结果缓存 60 秒，拓扑轮询高频调用无压力）
func InstalledAgentsGet() []InstalledAgent {
	installedAgentsMu.Lock()
	defer installedAgentsMu.Unlock()
	if installedAgentsCache != nil && time.Since(installedAgentsCachedAt) < 60*time.Second {
		return installedAgentsCache
	}
	result := make([]InstalledAgent, 0, len(AgentDefinitions))
	for _, def := range AgentDefinitions {
		installed := false
		for _, dir := range def.Dirs() {
			if dir == "" {
				continue
			}
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				installed = true
				break
			}
		}
		hasConfig := def.ConfigFiles != nil && len(def.ConfigFiles()) > 0
		result = append(result, InstalledAgent{Agent: def.ID, Name: def.Name, Installed: installed, HasConfig: hasConfig, AutoConfig: def.AutoConfigFileKey != ""})
	}
	installedAgentsCache = result
	installedAgentsCachedAt = time.Now()
	return result
}

// AgentDisplayName agent 标识的展示名，未知标识原样返回
func AgentDisplayName(id string) string {
	for _, def := range AgentDefinitions {
		if def.ID == id {
			return def.Name
		}
	}
	return id
}
