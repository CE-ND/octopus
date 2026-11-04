package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
)

const (
	marketplaceCacheTTL = 10 * time.Minute
	marketplacePageSize = 50
)

// MarketPluginInfo 是插件商城中展示的一个 npm 插件。
type MarketPluginInfo struct {
	ID               string `json:"id"`
	DisplayName      string `json:"display_name,omitempty"`
	Version          string `json:"version"`
	Description      string `json:"description,omitempty"`
	Publisher        string `json:"publisher,omitempty"`
	Spec             string `json:"spec"`
	Homepage         string `json:"homepage,omitempty"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installed_version,omitempty"`
	UpdateAvailable  bool   `json:"update_available,omitempty"`
}

type npmSearchResponse struct {
	Objects []struct {
		Package struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Description string   `json:"description"`
			Keywords    []string `json:"keywords"`
			Publisher   struct {
				Username string `json:"username"`
			} `json:"publisher"`
			Links struct {
				NPM      string `json:"npm"`
				Homepage string `json:"homepage"`
			} `json:"links"`
		} `json:"package"`
	} `json:"objects"`
}

// Marketplace 返回配置的 npm 用户/组织下可安装的 Octopus 插件。
// 结果缓存 10 分钟，refresh=true 时强制从 registry 刷新。
func (m *Manager) Marketplace(ctx context.Context, refresh bool) ([]MarketPluginInfo, error) {
	m.marketMu.Lock()
	defer m.marketMu.Unlock()

	if !refresh && !m.marketAt.IsZero() && time.Since(m.marketAt) < marketplaceCacheTTL {
		items := cloneMarketPlugins(m.marketCache)
		m.enrichMarketplace(items)
		return items, nil
	}

	users := marketplaceUsers()
	if len(users) == 0 {
		m.marketCache = []MarketPluginInfo{}
		m.marketAt = time.Now()
		return []MarketPluginInfo{}, nil
	}

	items, err := fetchMarketplace(ctx, conf.AppConfig.Plugin.Registry, users)
	if err != nil {
		// Keep the last successful result visible during a transient registry failure.
		if !m.marketAt.IsZero() {
			return cloneMarketPlugins(m.marketCache), nil
		}
		return nil, err
	}
	m.marketCache = cloneMarketPlugins(items)
	m.enrichMarketplace(items)
	m.marketAt = time.Now()
	return cloneMarketPlugins(items), nil
}

func marketplaceUsers() []string {
	raw := strings.TrimSpace(conf.AppConfig.Plugin.MarketplaceUser)
	if raw == "" {
		return nil
	}
	seen := make(map[string]bool)
	users := make([]string, 0, 2)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	}) {
		user := strings.TrimSpace(strings.TrimPrefix(part, "@"))
		if user == "" || seen[user] {
			continue
		}
		seen[user] = true
		users = append(users, user)
	}
	return users
}

func fetchMarketplace(ctx context.Context, registry string, users []string) ([]MarketPluginInfo, error) {
	registry = strings.TrimRight(strings.TrimSpace(registry), "/")
	if registry == "" {
		return nil, fmt.Errorf("plugin registry is empty")
	}
	client := &http.Client{Timeout: 12 * time.Second}
	seen := make(map[string]bool)
	items := make([]MarketPluginInfo, 0)
	for _, user := range users {
		searchURL := fmt.Sprintf("%s/-/v1/search?text=%s&size=%d", registry, url.QueryEscape("maintainer:"+user), marketplacePageSize)
		var search npmSearchResponse
		if err := getRegistryJSON(ctx, client, searchURL, &search); err != nil {
			return nil, fmt.Errorf("search npm packages for %s: %w", user, err)
		}
		for _, object := range search.Objects {
			pkg := object.Package
			name := strings.TrimSpace(pkg.Name)
			if name == "" || seen[name] || isStagedPlaceholder(pkg.Version) {
				continue
			}
			seen[name] = true

			manifest, manifestErr := fetchLatestManifest(ctx, client, registry, name)
			if manifestErr != nil && !looksLikeOctopusPlugin(name, pkg.Keywords, packageJSON{}) {
				continue
			}
			if manifestErr == nil {
				if manifest.Private || isStagedPlaceholder(manifest.Version) {
					continue
				}
				if !looksLikeOctopusPlugin(name, pkg.Keywords, manifest) {
					continue
				}
			}

			version := strings.TrimSpace(pkg.Version)
			if manifest.Version != "" {
				version = manifest.Version
			}
			if version == "" {
				continue
			}
			displayName := manifest.displayName()
			if displayName == "" {
				displayName = fallbackMarketDisplayName(name)
			}
			description := strings.TrimSpace(manifest.Description)
			if description == "" {
				description = strings.TrimSpace(pkg.Description)
			}
			homepage := strings.TrimSpace(manifest.Homepage)
			if homepage == "" {
				homepage = strings.TrimSpace(pkg.Links.Homepage)
			}
			items = append(items, MarketPluginInfo{
				ID:          name,
				DisplayName: displayName,
				Version:     version,
				Description: description,
				Publisher:   strings.TrimSpace(pkg.Publisher.Username),
				Spec:        name + "@" + version,
				Homepage:    homepage,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := strings.ToLower(items[i].DisplayName + items[i].ID)
		right := strings.ToLower(items[j].DisplayName + items[j].ID)
		return left < right
	})
	return items, nil
}

func fetchLatestManifest(ctx context.Context, client *http.Client, registry, name string) (packageJSON, error) {
	escaped := strings.ReplaceAll(url.PathEscape(name), "%40", "@")
	var pkg packageJSON
	if err := getRegistryJSON(ctx, client, registry+"/"+escaped+"/latest", &pkg); err != nil {
		return pkg, err
	}
	return pkg, nil
}

func getRegistryJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "octopus-plugin-marketplace")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry returned %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode registry response: %w", err)
	}
	return nil
}

func (m *Manager) enrichMarketplace(items []MarketPluginInfo) {
	m.mu.Lock()
	installed := make(map[string]InstalledPlugin, len(m.installed.Plugins))
	for _, p := range m.installed.Plugins {
		installed[p.ID] = p
	}
	m.mu.Unlock()

	for i := range items {
		items[i].Installed = false
		items[i].InstalledVersion = ""
		items[i].UpdateAvailable = false

		p, ok := installed[items[i].ID]
		if !ok {
			continue
		}
		items[i].Installed = true
		items[i].InstalledVersion = p.Version
		items[i].UpdateAvailable = p.Version != items[i].Version
	}
}

func looksLikeOctopusPlugin(name string, keywords []string, pkg packageJSON) bool {
	if strings.Contains(strings.ToLower(name), "octopus-plugin") {
		return true
	}
	for _, keyword := range keywords {
		if strings.EqualFold(strings.TrimSpace(keyword), "octopus-plugin") {
			return true
		}
	}
	return pkg.Octopus.DisplayName != "" ||
		len(pkg.Octopus.UI.ConfigSchema) > 0 ||
		len(pkg.Octopus.UI.Pages) > 0 ||
		len(pkg.Octopus.Replaces) > 0
}

func isStagedPlaceholder(version string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(version)), "-stage")
}

func fallbackMarketDisplayName(name string) string {
	base := name
	if index := strings.LastIndex(base, "/"); index >= 0 {
		base = base[index+1:]
	}
	base = strings.TrimPrefix(base, "octopus-plugin-")
	words := strings.FieldsFunc(base, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	})
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	if len(words) == 0 {
		return name
	}
	return strings.Join(words, " ")
}

func cloneMarketPlugins(items []MarketPluginInfo) []MarketPluginInfo {
	if len(items) == 0 {
		return []MarketPluginInfo{}
	}
	return append([]MarketPluginInfo(nil), items...)
}
