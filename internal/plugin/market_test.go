package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
)

func TestMarketplaceFromNPMRegistry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/-/v1/search") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"objects": []any{
					map[string]any{"package": map[string]any{
						"name":        "@cend/octopus-plugin-workbuddy-auth",
						"version":     "1.0.0",
						"description": "WorkBuddy provider plugin for Octopus",
						"publisher":   map[string]any{"username": "cend"},
					}},
					map[string]any{"package": map[string]any{
						"name":        "@cend/octopus-plugin-staged",
						"version":     "0.0.0-stage",
						"description": "Temporary package placeholder for staged publishing",
					}},
					map[string]any{"package": map[string]any{
						"name":    "@cend/unrelated-package",
						"version": "2.0.0",
					}},
				},
			})
			return
		}
		if strings.Contains(r.URL.Path, "unrelated-package") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/latest") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":        "@cend/octopus-plugin-workbuddy-auth",
				"version":     "1.0.0",
				"description": "WorkBuddy provider plugin for Octopus",
				"homepage":    "https://github.com/CE-ND/Octopus-plugin",
				"octopus": map[string]any{
					"displayName": "WorkBuddy",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	oldRegistry := conf.AppConfig.Plugin.Registry
	oldUser := conf.AppConfig.Plugin.MarketplaceUser
	conf.AppConfig.Plugin.Registry = server.URL
	conf.AppConfig.Plugin.MarketplaceUser = "cend"
	defer func() {
		conf.AppConfig.Plugin.Registry = oldRegistry
		conf.AppConfig.Plugin.MarketplaceUser = oldUser
	}()

	m := &Manager{installed: installedFile{Plugins: []InstalledPlugin{{
		ID:      "@cend/octopus-plugin-workbuddy-auth",
		Version: "0.9.0",
		Enabled: true,
	}}}}
	items, err := m.Marketplace(context.Background(), true)
	if err != nil {
		t.Fatalf("marketplace: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("marketplace items = %+v, want 1", items)
	}
	item := items[0]
	if item.ID != "@cend/octopus-plugin-workbuddy-auth" || item.Spec != "@cend/octopus-plugin-workbuddy-auth@1.0.0" {
		t.Fatalf("marketplace item = %+v", item)
	}
	if item.DisplayName != "WorkBuddy" {
		t.Fatalf("display name = %q", item.DisplayName)
	}
	if !item.Installed || item.InstalledVersion != "0.9.0" || !item.UpdateAvailable {
		t.Fatalf("installed state = %+v", item)
	}

	// Cached marketplace metadata must still reflect the current installed version.
	m.installed.Plugins[0].Version = "1.0.0"
	cached, err := m.Marketplace(context.Background(), false)
	if err != nil {
		t.Fatalf("cached marketplace: %v", err)
	}
	if len(cached) != 1 || !cached[0].Installed || cached[0].UpdateAvailable {
		t.Fatalf("cached installed state = %+v", cached)
	}
}

func TestMarketplaceRequiresConfiguredUser(t *testing.T) {
	oldUser := conf.AppConfig.Plugin.MarketplaceUser
	conf.AppConfig.Plugin.MarketplaceUser = ""
	defer func() { conf.AppConfig.Plugin.MarketplaceUser = oldUser }()

	m := &Manager{}
	items, err := m.Marketplace(context.Background(), true)
	if err != nil {
		t.Fatalf("marketplace without user: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("marketplace items = %+v, want empty", items)
	}
}
