package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/transformer/outbound"
	"github.com/bestruirui/octopus/internal/utils/log"
)

var (
	defaultManager *Manager
	managerOnce    sync.Once
)

// Manager 管理已安装插件、plugin-host 子进程和受管渠道。
type Manager struct {
	mu        sync.Mutex
	marketMu  sync.Mutex
	dataDir   string
	host      *Host
	hostErr   string
	installed installedFile
	enabled   bool
	nodePath  string

	marketCache []MarketPluginInfo
	marketAt    time.Time
}

// Default 返回进程级插件管理器。
func Default() *Manager {
	managerOnce.Do(func() {
		defaultManager = &Manager{
			dataDir:  pluginDataDir(),
			enabled:  conf.AppConfig.Plugin.Enabled,
			nodePath: resolveNodePath(),
		}
	})
	return defaultManager
}

func pluginDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("OCTOPUS_PLUGIN_DATA_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(conf.AppConfig.Plugin.DataDir); dir != "" {
		return dir
	}
	return "data/plugins"
}

func resolveNodePath() string {
	if p := strings.TrimSpace(conf.AppConfig.Plugin.NodePath); p != "" {
		return p
	}
	if p := strings.TrimSpace(os.Getenv("OCTOPUS_PLUGIN_NODE")); p != "" {
		return p
	}
	return ""
}

func (m *Manager) installedPath() string {
	return filepath.Join(m.dataDir, "installed.json")
}

func pathWithin(root, target string) bool {
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (m *Manager) loadInstalled() error {
	data, err := os.ReadFile(m.installedPath())
	if errors.Is(err, os.ErrNotExist) {
		m.installed = installedFile{Plugins: []InstalledPlugin{}}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read installed plugins: %w", err)
	}
	var f installedFile
	if err := json.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("parse installed plugins: %w", err)
	}
	if f.Plugins == nil {
		f.Plugins = []InstalledPlugin{}
	}
	m.installed = f
	return nil
}

func (m *Manager) saveInstalledLocked() error {
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		return err
	}
	sortPlugins(m.installed.Plugins)
	data, err := json.MarshalIndent(m.installed, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.installedPath() + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.installedPath())
}

// Init 初始化插件管理器。插件能力不可用时只记录错误，不影响 Octopus 启动。
func Init(ctx context.Context) error {
	m := Default()
	if !m.enabled {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		return err
	}
	if err := m.loadInstalled(); err != nil {
		log.Warnf("plugin: %v", err)
	}
	if err := m.ensureHostLocked(ctx); err != nil {
		m.hostErr = err.Error()
		log.Warnf("plugin: host not started: %v", err)
		return nil
	}
	if err := m.syncChannelsLocked(ctx); err != nil {
		log.Warnf("plugin: channel sync failed: %v", err)
	}
	return nil
}

// Shutdown 停止插件宿主。
func Shutdown() {
	m := Default()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopHostLocked()
}

func (m *Manager) hasEnabledLocked() bool {
	for _, p := range m.installed.Plugins {
		if p.Enabled {
			return true
		}
	}
	return false
}

func (m *Manager) ensureHostLocked(ctx context.Context) error {
	if !m.hasEnabledLocked() {
		m.stopHostLocked()
		m.hostErr = ""
		return nil
	}
	if m.host != nil && m.host.Running() {
		return nil
	}
	if m.host != nil {
		m.stopHostLocked()
	}
	if m.nodePath == "" {
		m.nodePath = resolveNodePath()
	}
	h, err := StartHost(ctx, HostOptions{
		DataDir:      m.dataDir,
		NodePath:     m.nodePath,
		NodeElectron: conf.AppConfig.Plugin.NodeElectron || strings.TrimSpace(os.Getenv("OCTOPUS_PLUGIN_NODE_ELECTRON")) == "1",
	})
	if err != nil {
		m.hostErr = err.Error()
		return err
	}
	m.host = h
	m.hostErr = ""
	return nil
}

func (m *Manager) stopHostLocked() {
	if m.host != nil {
		_ = m.host.Stop()
		m.host = nil
	}
}

func (m *Manager) restartHostLocked(ctx context.Context) error {
	m.stopHostLocked()
	return m.ensureHostLocked(ctx)
}

// Status 返回插件页所需的完整状态。
func (m *Manager) Status(ctx context.Context) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status{
		Enabled:     m.enabled,
		HostRunning: m.host != nil && m.host.Running(),
		HostError:   m.hostErr,
		Plugins:     []PluginInfo{},
		Providers:   []ProviderInfo{},
	}
	for _, p := range m.installed.Plugins {
		info := PluginInfo{
			ID:          p.ID,
			DisplayName: p.DisplayName,
			Spec:        p.Spec,
			Version:     p.Version,
			Enabled:     p.Enabled,
		}
		if cfg, err := pluginConfigInfo(p); err == nil {
			if cfg.DisplayName != "" {
				info.DisplayName = cfg.DisplayName
			}
			info.Options = cfg.Options
			info.ConfigSchema = cfg.ConfigSchema
			info.Pages = cfg.Pages
		} else {
			info.Error = err.Error()
		}
		status.Plugins = append(status.Plugins, info)
	}
	if m.host != nil && m.host.Running() {
		if plugins, err := m.host.Status(ctx); err == nil {
			byID := make(map[string]PluginInfo, len(plugins))
			for _, p := range plugins {
				byID[p.ID] = p
			}
			for i := range status.Plugins {
				if live, ok := byID[status.Plugins[i].ID]; ok {
					status.Plugins[i].Error = live.Error
					status.Plugins[i].Providers = live.Providers
				}
			}
		} else {
			status.HostError = err.Error()
		}
	}
	return status, nil
}

// Providers 返回插件 provider 与模型，并同步受管渠道。
func (m *Manager) Providers(ctx context.Context, refresh bool) ([]ProviderInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host == nil || !m.host.Running() {
		if err := m.ensureHostLocked(ctx); err != nil {
			return nil, err
		}
	}
	if m.host == nil || !m.host.Running() {
		_ = m.syncChannelsLocked(ctx)
		return []ProviderInfo{}, nil
	}
	providers, err := m.host.Providers(ctx, refresh)
	if err != nil {
		return nil, err
	}
	if err := m.syncChannelsLocked(ctx); err != nil {
		log.Warnf("plugin: channel sync failed: %v", err)
	}
	return providers, nil
}

// Install 安装一个 npm 包或本地插件目录。
func (m *Manager) Install(ctx context.Context, spec string) (InstalledPlugin, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Stop the host before replacing a plugin directory: Windows keeps loaded
	// files open and a rename over them fails while Node is reading them.
	m.stopHostLocked()
	installed, err := installPackage(ctx, m.dataDir, spec, conf.AppConfig.Plugin.Registry)
	if err != nil {
		_ = m.ensureHostLocked(ctx)
		return InstalledPlugin{}, err
	}
	if err := m.applyReplacesLocked(ctx, &installed); err != nil {
		_ = m.ensureHostLocked(ctx)
		return InstalledPlugin{}, err
	}
	found := false
	for i := range m.installed.Plugins {
		if m.installed.Plugins[i].ID == installed.ID {
			installed.Enabled = m.installed.Plugins[i].Enabled
			installed.Options = m.installed.Plugins[i].Options
			m.installed.Plugins[i] = installed
			found = true
			break
		}
	}
	if !found {
		m.installed.Plugins = append(m.installed.Plugins, installed)
	}
	if err := m.saveInstalledLocked(); err != nil {
		return InstalledPlugin{}, err
	}
	if err := m.restartHostLocked(ctx); err != nil {
		m.hostErr = err.Error()
	}
	_ = m.syncChannelsLocked(ctx)
	return installed, nil
}

// applyReplacesLocked 处理插件包声明的 octopus.replaces：
// 移除旧插件条目，把旧渠道绑定迁移到新插件 ID，并继承启用状态和配置项。
func (m *Manager) applyReplacesLocked(ctx context.Context, next *InstalledPlugin) error {
	if next == nil || len(next.Replaces) == 0 {
		return nil
	}
	replaced := make(map[string]bool, len(next.Replaces))
	for _, id := range next.Replaces {
		id = strings.TrimSpace(id)
		if id != "" && id != next.ID {
			replaced[id] = true
		}
	}
	if len(replaced) == 0 {
		return nil
	}
	kept := make([]InstalledPlugin, 0, len(m.installed.Plugins))
	var legacy *InstalledPlugin
	for _, p := range m.installed.Plugins {
		if !replaced[p.ID] {
			kept = append(kept, p)
			continue
		}
		copy := p
		if legacy == nil {
			legacy = &copy
		}
		if err := op.PluginChannelBindingRenamePlugin(p.ID, next.ID, ctx); err != nil {
			return fmt.Errorf("migrate plugin %s -> %s: %w", p.ID, next.ID, err)
		}
		// The old package directory is an installed copy under dataDir; remove it
		// after its entry and bindings have been migrated. Never touch a source
		// directory outside dataDir.
		if p.Dir != "" && pathWithin(m.dataDir, p.Dir) {
			if err := os.RemoveAll(p.Dir); err != nil {
				log.Warnf("plugin: remove replaced package %s: %v", p.Dir, err)
			}
		}
	}
	m.installed.Plugins = kept
	if legacy != nil {
		next.Enabled = legacy.Enabled
		if len(next.Options) == 0 {
			next.Options = legacy.Options
		}
		if next.InstalledAt.IsZero() {
			next.InstalledAt = legacy.InstalledAt
		}
	}
	return nil
}

// Toggle 启用或停用一个插件。
func (m *Manager) Toggle(ctx context.Context, id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for i := range m.installed.Plugins {
		if m.installed.Plugins[i].ID == id {
			m.installed.Plugins[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("plugin not found: %s", id)
	}
	if err := m.saveInstalledLocked(); err != nil {
		return err
	}
	if err := m.restartHostLocked(ctx); err != nil {
		m.hostErr = err.Error()
	}
	return m.syncChannelsLocked(ctx)
}

// Remove 卸载插件，并清理它管理的渠道。
func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keep []InstalledPlugin
	var removed *InstalledPlugin
	for i := range m.installed.Plugins {
		if m.installed.Plugins[i].ID == id {
			removed = &m.installed.Plugins[i]
			continue
		}
		keep = append(keep, m.installed.Plugins[i])
	}
	if removed == nil {
		return fmt.Errorf("plugin not found: %s", id)
	}
	m.installed.Plugins = keep
	if err := m.saveInstalledLocked(); err != nil {
		return err
	}
	m.stopHostLocked()
	if removed.Dir != "" && pathWithin(m.dataDir, removed.Dir) {
		_ = os.RemoveAll(removed.Dir)
	}
	if err := m.restartHostLocked(ctx); err != nil {
		m.hostErr = err.Error()
	}
	return m.syncChannelsLocked(ctx)
}

// PluginConfig 返回插件配置 schema、当前值和自定义页面。
func (m *Manager) PluginConfig(ctx context.Context, id string) (PluginConfigInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.installed.Plugins {
		if p.ID == id {
			return pluginConfigInfo(p)
		}
	}
	return PluginConfigInfo{}, fmt.Errorf("plugin not found: %s", id)
}

// SetOptions 保存插件配置；宿主会重启以让插件重新读取 options。
func (m *Manager) SetOptions(ctx context.Context, id string, options map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.installed.Plugins {
		if m.installed.Plugins[i].ID != id {
			continue
		}
		m.installed.Plugins[i].Options = options
		if err := m.saveInstalledLocked(); err != nil {
			return err
		}
		if err := m.restartHostLocked(ctx); err != nil {
			m.hostErr = err.Error()
			return err
		}
		return m.syncChannelsLocked(ctx)
	}
	return fmt.Errorf("plugin not found: %s", id)
}

// PluginPage 返回插件自定义页面的 HTML。
func (m *Manager) PluginPage(ctx context.Context, id, pageID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.installed.Plugins {
		if p.ID == id {
			return pluginPageHTML(p, pageID)
		}
	}
	return "", fmt.Errorf("plugin not found: %s", id)
}

// AuthMethods 返回某个 provider 的登录方式。
func (m *Manager) AuthMethods(ctx context.Context, pluginID, providerID string) ([]AuthMethod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host == nil || !m.host.Running() {
		return nil, errors.New("plugin host is not running")
	}
	return m.host.AuthMethods(ctx, pluginID, providerID)
}

// StartLogin 开始一次插件登录。
func (m *Manager) StartLogin(ctx context.Context, req AuthStartRequest) (AuthSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host == nil || !m.host.Running() {
		return AuthSession{}, errors.New("plugin host is not running")
	}
	return m.host.StartLogin(ctx, req)
}

// LoginStatus 查询登录会话；成功时触发渠道同步。
func (m *Manager) LoginStatus(ctx context.Context, sessionID string) (LoginStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host == nil || !m.host.Running() {
		return LoginStatus{}, errors.New("plugin host is not running")
	}
	status, err := m.host.LoginStatus(ctx, sessionID)
	if err != nil {
		return status, err
	}
	if status.Status == "success" {
		_ = m.syncChannelsLocked(ctx)
	}
	return status, nil
}

// Logout 清除某个 provider 的登录凭据。
func (m *Manager) Logout(ctx context.Context, pluginID, providerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host == nil || !m.host.Running() {
		return errors.New("plugin host is not running")
	}
	if err := m.host.Logout(ctx, pluginID, providerID); err != nil {
		return err
	}
	return m.syncChannelsLocked(ctx)
}

// RefreshModels 刷新模型并同步渠道。
func (m *Manager) RefreshModels(ctx context.Context, pluginID, providerID string) ([]ProviderInfo, error) {
	return m.Providers(ctx, true)
}

// Logs 返回 plugin-host 日志的最后 n 行。
func (m *Manager) Logs(n int) ([]string, error) {
	if n <= 0 || n > 2000 {
		n = 200
	}
	data, err := os.ReadFile(filepath.Join(m.dataDir, "host.log"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// syncChannelsLocked 根据插件 provider 路由创建/更新受管渠道。
func (m *Manager) syncChannelsLocked(ctx context.Context) error {
	providers := []ProviderInfo{}
	if m.host != nil && m.host.Running() {
		var err error
		providers, err = m.host.Providers(ctx, false)
		if err != nil {
			return err
		}
	}
	type desired struct {
		key        string
		name       string
		channel    model.Channel
		protocol   string
		pluginID   string
		providerID string
		routeID    string
	}
	desiredByKey := make(map[string]desired)
	for _, provider := range providers {
		if !provider.LoggedIn || len(provider.Routes) == 0 {
			continue
		}
		multiRoute := len(provider.Routes) > 1
		for _, route := range provider.Routes {
			route := route
			if strings.TrimSpace(route.BaseURL) == "" || len(route.Models) == 0 {
				continue
			}
			protocol := strings.ToLower(strings.TrimSpace(route.Protocol))
			if protocol == "" {
				protocol = "openai-chat"
			}
			outboundType := outbound.OutboundTypeOpenAIChat
			label := "Chat"
			switch protocol {
			case "anthropic", "anthropic-messages":
				outboundType = outbound.OutboundTypeAnthropic
				label = "Anthropic"
			}
			name := "Plugin: " + provider.Name
			if multiRoute {
				name += " (" + label + ")"
			}
			baseURL := strings.TrimRight(m.host.BaseURL(), "/") + "/p/" +
				url.PathEscape(route.ID) + "/v1"
			key := provider.PluginID + "|" + provider.ProviderID + "|" + route.ID
			desiredByKey[key] = desired{
				key:  key,
				name: name,
				channel: model.Channel{
					Name:      name,
					Type:      outboundType,
					Enabled:   provider.LoggedIn,
					BaseUrls:  []model.BaseUrl{{URL: baseURL}},
					Model:     strings.Join(route.Models, ","),
					ProxyMode: model.ProxyUsageModeDirect,
				},
				protocol:   protocol,
				pluginID:   provider.PluginID,
				providerID: provider.ProviderID,
				routeID:    route.ID,
			}
		}
	}

	existing, err := op.PluginChannelBindingList(ctx)
	if err != nil {
		return err
	}
	existingByKey := make(map[string]model.PluginChannelBinding, len(existing))
	for _, binding := range existing {
		existingByKey[binding.PluginID+"|"+binding.ProviderID+"|"+binding.RouteID] = binding
	}
	installedPlugins := make(map[string]bool, len(m.installed.Plugins))
	for _, p := range m.installed.Plugins {
		installedPlugins[p.ID] = p.Enabled
	}

	for key, binding := range existingByKey {
		if _, ok := desiredByKey[key]; ok {
			continue
		}
		// A disabled plugin keeps its channels (disabled) so re-enabling it
		// does not lose the user's group configuration. Only an uninstalled
		// plugin deletes them.
		if _, installed := installedPlugins[binding.PluginID]; installed {
			emptyModels := ""
			disabled := false
			if _, err := op.ChannelUpdate(&model.ChannelUpdateRequest{
				ID:                 binding.ChannelID,
				Enabled:            &disabled,
				Model:              &emptyModels,
				BypassManagedCheck: true,
			}, ctx); err != nil {
				log.Warnf("plugin: disable channel %d: %v", binding.ChannelID, err)
			}
			continue
		}
		if err := op.ChannelDelPlugin(binding.ChannelID, ctx); err != nil {
			log.Warnf("plugin: delete stale channel %d: %v", binding.ChannelID, err)
		}
		_ = op.PluginChannelBindingDeleteByChannelID(binding.ChannelID, ctx)
	}

	for key, d := range desiredByKey {
		if binding, ok := existingByKey[key]; ok {
			channel, err := op.ChannelGet(binding.ChannelID, ctx)
			if err != nil || channel == nil {
				_ = op.PluginChannelBindingDeleteByChannelID(binding.ChannelID, ctx)
			} else {
				name := d.name
				enabled := d.channel.Enabled
				baseURLs := d.channel.BaseUrls
				modelNames := d.channel.Model
				outboundType := d.channel.Type
				if _, err := op.ChannelUpdate(&model.ChannelUpdateRequest{
					ID:                 binding.ChannelID,
					Name:               &name,
					Type:               &outboundType,
					Enabled:            &enabled,
					BaseUrls:           &baseURLs,
					Model:              &modelNames,
					BypassManagedCheck: true,
				}, ctx); err != nil {
					log.Warnf("plugin: update channel %d: %v", binding.ChannelID, err)
				} else {
					binding.Protocol = d.protocol
					_ = op.PluginChannelBindingSave(&binding, ctx)
				}
				continue
			}
		}
		payload := d.channel
		payload.Keys = []model.ChannelKey{{
			Enabled:    true,
			ChannelKey: m.host.Token(),
			Remark:     "plugin",
		}}
		if err := op.ChannelCreate(&payload, ctx); err != nil {
			log.Warnf("plugin: create channel for %s: %v", key, err)
			continue
		}
		binding := model.PluginChannelBinding{
			PluginID:   d.pluginID,
			ProviderID: d.providerID,
			RouteID:    d.routeID,
			Protocol:   d.protocol,
			ChannelID:  payload.ID,
		}
		if err := op.PluginChannelBindingSave(&binding, ctx); err != nil {
			log.Warnf("plugin: save channel binding: %v", err)
		}
	}
	return nil
}
