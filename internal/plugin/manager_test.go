package plugin

import (
	"context"
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

func TestApplyPluginReplacesPreservesState(t *testing.T) {
	root, err := os.MkdirTemp("", "octopus-plugin-replaces-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := db.InitDB("sqlite", filepath.Join(root, "data.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer db.Close()

	const oldID = "@octopus/plugin-workbuddy-auth"
	const newID = "@cend/octopus-plugin-workbuddy-auth"
	m := &Manager{
		dataDir: filepath.Join(root, "plugins"),
		installed: installedFile{Plugins: []InstalledPlugin{{
			ID:      oldID,
			Enabled: false,
			Options: map[string]any{"free_only": true},
		}}},
	}
	binding := model.PluginChannelBinding{
		PluginID:   oldID,
		ProviderID: "workbuddy",
		RouteID:    "route-1",
		Protocol:   "openai-chat",
		ChannelID:  42,
	}
	if err := op.PluginChannelBindingSave(&binding, context.Background()); err != nil {
		t.Fatalf("save binding: %v", err)
	}

	next := InstalledPlugin{ID: newID, Enabled: true, Replaces: []string{oldID}}
	if err := m.applyReplacesLocked(context.Background(), &next); err != nil {
		t.Fatalf("apply replaces: %v", err)
	}
	if len(m.installed.Plugins) != 0 {
		t.Fatalf("old plugin entry remained: %+v", m.installed.Plugins)
	}
	if next.Enabled {
		t.Fatal("enabled state was not inherited")
	}
	if next.Options["free_only"] != true {
		t.Fatalf("options were not inherited: %+v", next.Options)
	}
	migrated, ok, err := op.PluginChannelBindingGetByChannelID(42, context.Background())
	if err != nil || !ok {
		t.Fatalf("binding after replace: ok=%v err=%v", ok, err)
	}
	if migrated.PluginID != newID {
		t.Fatalf("binding plugin ID = %q, want %q", migrated.PluginID, newID)
	}
}

func TestManagerInstallFakePlugin(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	root, err := os.MkdirTemp("", "octopus-plugin-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	conf.AppConfig.Plugin.Enabled = true
	conf.AppConfig.Plugin.NodePath = nodePath
	if err := db.InitDB("sqlite", filepath.Join(root, "data.db"), false); err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer db.Close()
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %q", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("upstream authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`)
	}))
	defer upstream.Close()
	t.Setenv("FAKE_UPSTREAM", upstream.URL+"/v1")

	pluginDir, err := filepath.Abs(filepath.Join("testdata", "fake"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{
		dataDir:  filepath.Join(root, "plugins"),
		enabled:  true,
		nodePath: nodePath,
	}
	defer m.stopHostLocked()

	if _, err := m.Install(context.Background(), pluginDir); err != nil {
		t.Fatalf("install: %v", err)
	}
	cfgInfo, err := m.PluginConfig(context.Background(), "@octopus/fake-plugin")
	if err != nil {
		t.Fatalf("plugin config: %v", err)
	}
	if len(cfgInfo.ConfigSchema) == 0 || len(cfgInfo.Pages) != 1 {
		t.Fatalf("plugin config schema/pages missing: %+v", cfgInfo)
	}
	if err := m.SetOptions(context.Background(), "@octopus/fake-plugin", map[string]any{"free_only": true}); err != nil {
		t.Fatalf("set plugin options: %v", err)
	}
	if cfgInfo, err = m.PluginConfig(context.Background(), "@octopus/fake-plugin"); err != nil || cfgInfo.Options["free_only"] != true {
		t.Fatalf("plugin options not persisted: %+v err=%v", cfgInfo, err)
	}
	if pageHTML, err := m.PluginPage(context.Background(), "@octopus/fake-plugin", "demo"); err != nil || !strings.Contains(pageHTML, "Fake plugin page") {
		t.Fatalf("plugin page: %q err=%v", pageHTML, err)
	}
	unsignedProviders, err := m.Providers(context.Background(), false)
	if err != nil {
		t.Fatalf("unsigned providers: %v", err)
	}
	if len(unsignedProviders) != 1 || len(unsignedProviders[0].Models) != 0 || len(unsignedProviders[0].Routes) != 0 {
		t.Fatalf("unsigned provider should expose no usable models: %+v", unsignedProviders)
	}
	if channels, err := op.ChannelList(context.Background()); err != nil || len(channels) != 0 {
		t.Fatalf("unsigned provider should not create a channel: %+v err=%v", channels, err)
	}
	if _, err := m.StartLogin(context.Background(), AuthStartRequest{
		PluginID:   "@octopus/fake-plugin",
		ProviderID: "fake",
		Method:     0,
		APIKey:     "sk-test",
	}); err != nil {
		t.Fatalf("start login: %v", err)
	}
	providers, err := m.Providers(context.Background(), false)
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(providers))
	}
	if len(providers[0].Models) != 1 || providers[0].Models[0].ID != "fake-model" {
		t.Fatalf("models = %+v", providers[0].Models)
	}
	if len(providers[0].Routes) != 1 {
		t.Fatalf("routes = %+v", providers[0].Routes)
	}
	if !providers[0].LoggedIn {
		t.Fatalf("provider should be logged in after API key sign-in")
	}

	channels, err := op.ChannelList(context.Background())
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("channels = %d, want 1", len(channels))
	}
	if channels[0].Model != "fake-model" {
		t.Fatalf("channel model = %q", channels[0].Model)
	}
	if !channels[0].Enabled {
		t.Fatalf("channel should be enabled after sign-in")
	}
	if _, ok, err := op.PluginChannelBindingGetByChannelID(channels[0].ID, context.Background()); err != nil || !ok {
		t.Fatalf("binding: ok=%v err=%v", ok, err)
	}
	if len(channels[0].Keys) == 0 {
		t.Fatal("channel has no key")
	}
	hostKey := channels[0].Keys[0].ChannelKey
	body := strings.NewReader(`{"model":"fake-model","messages":[{"role":"user","content":"ping"}]}`)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(channels[0].BaseUrls[0].URL, "/")+"/chat/completions", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+channels[0].Keys[0].ChannelKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Expect", "100-continue")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("plugin data plane: %v", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(payload), "pong") {
		t.Fatalf("plugin data plane status=%d body=%s", resp.StatusCode, payload)
	}

	if err := m.Toggle(context.Background(), "@octopus/fake-plugin", false); err != nil {
		t.Fatalf("disable plugin: %v", err)
	}
	if providers, err := m.Providers(context.Background(), false); err != nil || len(providers) != 0 {
		t.Fatalf("providers while disabled = %+v err=%v", providers, err)
	}
	channels, err = op.ChannelList(context.Background())
	if err != nil || len(channels) != 1 || channels[0].Enabled {
		t.Fatalf("disabled plugin channel = %+v err=%v", channels, err)
	}
	if err := m.Toggle(context.Background(), "@octopus/fake-plugin", true); err != nil {
		t.Fatalf("enable plugin: %v", err)
	}
	channels, err = op.ChannelList(context.Background())
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels after enable = %+v err=%v", channels, err)
	}
	if channels[0].Keys[0].ChannelKey != hostKey {
		t.Fatalf("host channel key changed across restart: got %q want %q", channels[0].Keys[0].ChannelKey, hostKey)
	}
	if err := m.Remove(context.Background(), "@octopus/fake-plugin"); err != nil {
		t.Fatalf("remove plugin: %v", err)
	}
	channels, err = op.ChannelList(context.Background())
	if err != nil || len(channels) != 0 {
		t.Fatalf("channels after remove = %+v err=%v", channels, err)
	}
}
