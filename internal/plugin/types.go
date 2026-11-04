package plugin

import (
	"encoding/json"
	"time"
)

// InstalledPlugin 是 installed.json 中的一个插件条目。
// Dir 是插件包目录的绝对路径，由安装器写入，避免宿主依赖工作目录。
type InstalledPlugin struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name,omitempty"`
	Spec        string         `json:"spec"`
	Version     string         `json:"version"`
	Dir         string         `json:"dir"`
	Enabled     bool           `json:"enabled"`
	Options     map[string]any `json:"options,omitempty"`
	Replaces    []string       `json:"replaces,omitempty"`
	InstalledAt time.Time      `json:"installed_at"`
}

type installedFile struct {
	Plugins []InstalledPlugin `json:"plugins"`
}

// ProviderInfo 是插件宿主暴露给 Octopus 的 provider 状态。
type ProviderInfo struct {
	PluginID   string      `json:"plugin_id"`
	ProviderID string      `json:"provider_id"`
	Name       string      `json:"name"`
	LoggedIn   bool        `json:"logged_in"`
	Account    string      `json:"account,omitempty"`
	Error      string      `json:"error,omitempty"`
	Models     []ModelInfo `json:"models"`
	Routes     []RouteInfo `json:"routes"`
}

// ModelInfo 是插件 provider 的一个模型。
type ModelInfo struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Protocol     string         `json:"protocol"`
	Target       string         `json:"target,omitempty"`
	Context      int            `json:"context,omitempty"`
	Output       int            `json:"output,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
	Free         bool           `json:"free,omitempty"`
}

// RouteInfo 是一组共享同一个上游地址和协议的模型。
// Octopus 为每个 route 建一个受管渠道。
type RouteInfo struct {
	ID       string   `json:"id"`
	Protocol string   `json:"protocol"`
	BaseURL  string   `json:"base_url"`
	Models   []string `json:"models"`
}

// PluginUIPage 是插件声明的自定义页面。
type PluginUIPage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Entry string `json:"entry"`
	Slot  string `json:"slot,omitempty"`
}

// PluginInfo 是插件列表中的展示信息。
type PluginInfo struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name,omitempty"`
	Spec         string          `json:"spec"`
	Version      string          `json:"version"`
	Enabled      bool            `json:"enabled"`
	Error        string          `json:"error,omitempty"`
	Providers    []string        `json:"providers,omitempty"`
	Options      map[string]any  `json:"options,omitempty"`
	ConfigSchema json.RawMessage `json:"config_schema,omitempty"`
	Pages        []PluginUIPage  `json:"pages,omitempty"`
}

// PluginConfigInfo 是插件配置页需要的 schema 和当前值。
type PluginConfigInfo struct {
	PluginID     string          `json:"plugin_id"`
	DisplayName  string          `json:"display_name,omitempty"`
	ConfigSchema json.RawMessage `json:"config_schema,omitempty"`
	Options      map[string]any  `json:"options,omitempty"`
	Pages        []PluginUIPage  `json:"pages,omitempty"`
}

// Status 是插件管理页的完整状态。
type Status struct {
	Enabled     bool           `json:"enabled"`
	HostRunning bool           `json:"host_running"`
	HostError   string         `json:"host_error,omitempty"`
	Plugins     []PluginInfo   `json:"plugins"`
	Providers   []ProviderInfo `json:"providers"`
}

// AuthMethod 是插件声明的一种登录方式。
type AuthMethod struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
}

// AuthStartRequest 开始一次登录。
type AuthStartRequest struct {
	PluginID   string `json:"plugin_id"`
	ProviderID string `json:"provider_id"`
	Method     int    `json:"method"`
	APIKey     string `json:"api_key,omitempty"`
}

// AuthSession 是一次登录会话。
type AuthSession struct {
	ID           string `json:"id"`
	Status       string `json:"status"` // pending / success / failed
	URL          string `json:"url,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	Error        string `json:"error,omitempty"`
	Account      string `json:"account,omitempty"`
}

// LoginStatus 是登录轮询结果。
type LoginStatus struct {
	Status  string `json:"status"`
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
	Account string `json:"account,omitempty"`
}

// ProvidersResponse 是插件宿主 /providers 的响应。
type ProvidersResponse struct {
	Plugins   []PluginInfo   `json:"plugins"`
	Providers []ProviderInfo `json:"providers"`
}

// HostStatusResponse 是插件宿主 /status 的轻量响应。
type HostStatusResponse struct {
	Plugins []PluginInfo `json:"plugins"`
}
