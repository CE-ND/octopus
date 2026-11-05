package plugin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

func TestManagedPluginOwnsStateAndDescribesChannels(t *testing.T) {
	ctx := context.Background()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	resolved, err := exec.Command(node, "-p", "process.execPath").Output()
	if err != nil {
		t.Fatal(err)
	}
	node = strings.TrimSpace(string(resolved))
	root := t.TempDir()
	if err := db.InitDB("sqlite", filepath.Join(root, "data.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	old := conf.AppConfig.Plugin
	t.Cleanup(func() { conf.AppConfig.Plugin = old })
	conf.AppConfig.Plugin.Enabled = true
	conf.AppConfig.Plugin.NodePath = node
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Test-Plugin-Credential") != "fixture-private-key" {
			t.Error("plugin did not own the upstream request")
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"managed reply"}]}`)
	}))
	defer upstream.Close()
	t.Setenv("MANAGED_TEST_UPSTREAM", upstream.URL+"/v1")
	m := &Manager{dataDir: filepath.Join(root, "plugins"), enabled: true, nodePath: node}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.dataDir, "auth.json"), []byte(`{"legacy-managed-resource":{"type":"api","key":"fixture-private-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Remove(ctx, "@octopus/managed-test"); m.stopHostLocked() })
	dir, _ := filepath.Abs(filepath.Join("testdata", "managed"))
	if _, err := m.Install(ctx, dir); err != nil {
		t.Fatal(err)
	}
	providers, err := m.Providers(ctx, false)
	if err != nil || len(providers) != 1 || !providers[0].PluginManaged {
		t.Fatalf("managed descriptors: %+v %v", providers, err)
	}
	channels, err := op.ChannelList(ctx)
	if err != nil || len(channels) != 1 {
		t.Fatalf("managed channels: %+v %v", channels, err)
	}
	channel := channels[0]
	group := model.Group{Name: "managed-group", Mode: model.GroupModeRoundRobin, Items: []model.GroupItem{{ChannelID: channel.ID, ModelName: "managed-model"}}}
	if err := op.GroupCreate(&group, ctx); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, channel.BaseUrls[0].URL+"/messages", strings.NewReader(`{"model":"managed-model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+channel.Keys[0].ChannelKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(body), "managed reply") {
		t.Fatalf("managed forwarding: %d %s", response.StatusCode, body)
	}
	if data, err := os.ReadFile(filepath.Join(m.dataDir, "auth.json")); err == nil && strings.Contains(string(data), "fixture-private-key") {
		t.Fatal("host persisted the plugin credential")
	}
	if err := m.Logout(ctx, "@octopus/managed-test", "managed-resource"); err == nil {
		t.Fatal("host must not take over plugin-owned authentication")
	}
	change := func(enabled bool) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"enabled": enabled, "name": "Updated managed resource"})
		result, err := m.Call(ctx, CallRequest{PluginID: "@octopus/managed-test", Method: "resource.update", Params: params})
		if err != nil || !result.Refresh {
			t.Fatalf("plugin call: %+v %v", result, err)
		}
		saved, err := op.ChannelGet(channel.ID, ctx)
		if err != nil || saved.Enabled != enabled {
			t.Fatalf("channel did not reflect plugin description: %+v %v", saved, err)
		}
		savedGroup, err := op.GroupGet(group.ID, ctx)
		if err != nil || len(savedGroup.Items) != 1 || savedGroup.Items[0].ChannelID != channel.ID {
			t.Fatal("plugin description changes lost group membership")
		}
	}
	change(false)
	change(true)
	if err := m.Toggle(ctx, "@octopus/managed-test", false); err != nil {
		t.Fatal(err)
	}
	if err := m.Toggle(ctx, "@octopus/managed-test", true); err != nil {
		t.Fatal(err)
	}
	read, err := m.Call(ctx, CallRequest{PluginID: "@octopus/managed-test", Method: "resource.read"})
	if err != nil || !strings.Contains(string(read.Data), "Updated managed resource") {
		t.Fatalf("plugin private state did not survive restart: %s %v", read.Data, err)
	}
}
