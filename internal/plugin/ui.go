package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveJSONAsset accepts either an inline JSON object or a path relative to
// the plugin package. It keeps plugin UI manifests data-only; no plugin code
// runs while the schema is read.
func resolveJSONAsset(dir string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var path string
	if err := json.Unmarshal(raw, &path); err == nil {
		if strings.TrimSpace(path) == "" {
			return nil, nil
		}
		full := filepath.Join(dir, filepath.FromSlash(path))
		if !pathWithin(dir, full) {
			return nil, fmt.Errorf("plugin UI asset escapes package: %s", path)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("read plugin UI asset %s: %w", path, err)
		}
		if !json.Valid(data) {
			return nil, fmt.Errorf("plugin UI asset %s is not valid JSON", path)
		}
		return json.RawMessage(data), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("plugin config schema is not valid JSON")
	}
	return raw, nil
}

func pluginConfigInfo(p InstalledPlugin) (PluginConfigInfo, error) {
	pkg, err := readPackageJSON(p.Dir)
	if err != nil {
		return PluginConfigInfo{PluginID: p.ID, Options: p.Options}, err
	}
	schema, err := resolveJSONAsset(p.Dir, pkg.Octopus.UI.ConfigSchema)
	if err != nil {
		return PluginConfigInfo{PluginID: p.ID, Options: p.Options}, err
	}
	return PluginConfigInfo{
		PluginID:     p.ID,
		DisplayName:  pkg.displayName(),
		ConfigSchema: schema,
		Options:      p.Options,
		Pages:        pkg.Octopus.UI.Pages,
	}, nil
}

func pluginPageHTML(p InstalledPlugin, pageID string) (string, error) {
	pkg, err := readPackageJSON(p.Dir)
	if err != nil {
		return "", err
	}
	for _, page := range pkg.Octopus.UI.Pages {
		if page.ID != pageID {
			continue
		}
		full := filepath.Join(p.Dir, filepath.FromSlash(page.Entry))
		if !pathWithin(p.Dir, full) {
			return "", fmt.Errorf("plugin page escapes package: %s", page.Entry)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return "", fmt.Errorf("read plugin page %s: %w", page.Entry, err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("plugin page not found: %s", pageID)
}
