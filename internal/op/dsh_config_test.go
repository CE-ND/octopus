package op

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	yaml "go.yaml.in/yaml/v3"
)

const dshTestProfile = `# DSH user settings
- id: agent-default-model
  name: '@deepseek-ai/dsh-agent-default-model'
  config:
    provider: tokenhub
    model: old-model
    reasoningEffort: max
- id: ui-theme
  config:
    preference: dark
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      octopus:
        api: openai-responses
        apiKeyEnv: OCTOPUS_API_KEY
        baseURL: https://old.example/v1
        models:
          - id: existing
            contextWindow: 200000
            input: [text, image]
            reasoningEfforts:
              high: high
      tokenhub:
        api: anthropic-messages
        baseURL: https://tokenhub.example
        apiKeyEnv: TOKENHUB_API_KEY
        models:
          - id: old-model
`
const dshTestCredentials = `version: 1
# Other references must survive
refs:
  OCTOPUS_API_KEY: sk-auto-test
  TOKENHUB_API_KEY: other-provider-secret
records:
  client-connection/browser-session:
    kind: grant
    payload:
      secret: browser-session-secret
  deepseek-account-platform/default:
    kind: grant
    payload:
      token: account-secret
foreignSetting: keep
`

const dshDuplicateProfile = `# DSH layered patches
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      octopus:
        api: openai-responses
        apiKeyEnv: OCTOPUS_DESKTOP_API_KEY
        baseURL: http://127.0.0.1:18777/v1
        displayName: Octopus
        models:
          - id: octopus-model
            name: octopus-model
- id: agent-default-model
  config:
    provider: octopus
    model: octopus-model
- id: llm-pi-ai # magpie
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      magpie:
        displayName: Magpie
        apiKeyEnv: MAGPIE_GATEWAY_KEY
        api: openai-completions
        baseURL: http://127.0.0.1:3425/v1
        models:
          - id: workbuddy/deepseek-v4.1-flash
            name: DeepSeek-V4.1-Flash
- id: agent-default-model # magpie
  config:
    provider: magpie
    model: workbuddy/deepseek-v4.1-flash
`

func dshTestHome(t *testing.T) dshPaths {
	t.Helper()
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	paths, err := resolveDshPaths("desktop")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.directory, 0700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{paths.base: "[]\n", paths.profile: dshTestProfile, paths.credentials: dshTestCredentials} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func seedDshGroup(t *testing.T) (int, int) {
	t.Helper()
	keyID := seedAutoConfigKey(t)
	if err := db.GetDB().AutoMigrate(&model.Group{}, &model.GroupItem{}); err != nil {
		t.Fatal(err)
	}
	group := model.Group{ID: 876543, Name: "public-group-alias", Mode: model.GroupModeRoundRobin}
	if err := GroupCreate(&group, context.Background()); err != nil {
		t.Fatal(err)
	}
	setTestGateway()
	return keyID, group.ID
}

func dshTestPrepare(t *testing.T, keyID, groupID int, api string) model.AgentAutoConfigResponse {
	t.Helper()
	prepared, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: dshAgentID, APIKeyID: keyID, DSH: &model.DshAutoConfigOptions{Profile: "desktop", ModelGroupID: groupID, API: api}}, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func dshTestSaveRequest(t *testing.T, prepared model.AgentAutoConfigResponse, keyID int) model.AgentConfigSaveRequest {
	t.Helper()
	files, _, err := DshConfigList("desktop", context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return model.AgentConfigSaveRequest{Agent: dshAgentID, Files: []model.AgentConfigSaveFile{{Key: "profile", Content: prepared.Content, BaseModifiedAt: files[0].ModifiedAt}}, DSH: &model.DshConfigSaveOptions{Profile: "desktop", APIKeyID: keyID, ModelGroupID: prepared.DSH.ModelGroupID, CredentialRevision: prepared.DSH.CredentialRevision}}
}

func TestDshPrepareAndSavePreservesOtherProvidersAndCredentials(t *testing.T) {
	paths := dshTestHome(t)
	keyID, groupID := seedDshGroup(t)
	files, state, err := DshConfigList("", context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Key != "profile" || state.APIKeyID != keyID || state.CurrentProvider != "tokenhub" {
		t.Fatalf("unexpected public state: %+v", state)
	}
	public, _ := json.Marshal(struct {
		Files []AgentConfigFileView
		State *model.DshConfigState
	}{files, state})
	for _, secret := range []string{"sk-auto-test", "browser-session-secret", "account-secret", "other-provider-secret"} {
		if strings.Contains(string(public), secret) {
			t.Fatal("public state leaked a credential")
		}
	}
	prepared := dshTestPrepare(t, keyID, groupID, "")
	if prepared.DSH.API != "openai-responses" || prepared.DSH.ReasoningReset || prepared.DSH.ReasoningEffort != "max" {
		t.Fatal("existing API and reasoning compatibility were not handled")
	}
	for _, path := range []string{paths.profile, paths.credentials} {
		data, _ := os.ReadFile(path)
		expected := dshTestProfile
		if path == paths.credentials {
			expected = dshTestCredentials
		}
		if string(data) != expected {
			t.Fatal("preparation wrote a file")
		}
	}
	for _, text := range []string{"# DSH user settings", "tokenhub:", "contextWindow: 200000", "preference: dark", "public-group-alias", "OCTOPUS_DESKTOP_API_KEY"} {
		if !strings.Contains(prepared.Content, text) {
			t.Fatalf("missing preserved/generated field %s", text)
		}
	}
	if !strings.Contains(prepared.Content, "reasoningEffort: max") || strings.Contains(prepared.Content, "sk-auto-test") {
		t.Fatal("supported reasoning was lost or a credential was inlined")
	}
	again, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: dshAgentID, APIKeyID: keyID, DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID}, CurrentContent: &prepared.Content}, context.Background())
	if err != nil || again.Content != prepared.Content {
		t.Fatalf("preparation not idempotent: %v", err)
	}
	if err := DshConfigSave(dshTestSaveRequest(t, prepared, keyID), context.Background()); err != nil {
		t.Fatal(err)
	}
	credentialsText, _ := os.ReadFile(paths.credentials)
	doc, err := parseDshCredentials(string(credentialsText))
	if err != nil || dshCredentialValue(doc, dshManagedReference) != "sk-auto-test" {
		t.Fatal("managed credential not saved")
	}
	for _, text := range []string{"account-secret", "browser-session-secret", "other-provider-secret", "foreignSetting: keep", "# Other references"} {
		if !strings.Contains(string(credentialsText), text) {
			t.Fatalf("lost untouched credential field %s", text)
		}
	}
	_, state, err = DshConfigList("desktop", context.Background())
	if err != nil || state.CurrentProvider != "octopus" || state.CurrentModel != "public-group-alias" || state.APIKeyID != keyID {
		t.Fatal("saved state not recognized")
	}
	for _, path := range []string{paths.profile, paths.credentials} {
		if _, err := os.Stat(path + ".bak"); err != nil {
			t.Fatal("backup missing")
		}
	}
}

func TestDshProtocolAddressesAndKeyValidation(t *testing.T) {
	dshTestHome(t)
	keyID, groupID := seedDshGroup(t)
	for _, api := range []string{"openai-completions", "openai-responses", "anthropic-messages"} {
		prepared := dshTestPrepare(t, keyID, groupID, api)
		doc, _ := parseDshYAML(prepared.Content, yaml.SequenceNode)
		p := &dshProfile{patch: doc, base: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode}}}}
		provider, _ := p.provider(false)
		expected := "http://127.0.0.1:18777"
		if api != "anthropic-messages" {
			expected += "/v1"
		}
		if dshText(dshGet(provider, "baseURL")) != expected {
			t.Fatal("wrong API prefix")
		}
		item, err := dshModel(provider, prepared.DSH.ModelName)
		if err != nil {
			t.Fatal(err)
		}
		input := dshGet(item, "input")
		var modalities []string
		if input == nil || input.Decode(&modalities) != nil || !slices.Equal(modalities, []string{"text", "image"}) {
			t.Fatalf("%s model must allow text and image input, got %v", api, modalities)
		}
	}
	key, _ := APIKeyGet(keyID, context.Background())
	for _, scenario := range []string{"disabled", "expired", "restricted"} {
		candidate := key
		switch scenario {
		case "disabled":
			candidate.Enabled = false
		case "expired":
			candidate.ExpireAt = time.Now().Unix() - 30
		case "restricted":
			candidate.SupportedModels = "different-alias"
		}
		if err := APIKeyUpdate(&candidate, context.Background()); err != nil {
			t.Fatal(err)
		}
		_, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: dshAgentID, APIKeyID: keyID, DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID}}, context.Background())
		if err == nil {
			t.Fatalf("accepted %s key", scenario)
		}
	}
}

func TestDshAutoConfigBackfillsModelInputAndPreservesExplicitCapabilities(t *testing.T) {
	existing := strings.Replace(dshTestProfile, "- id: existing", "- id: public-group-alias", 1)
	for _, tt := range []struct {
		name    string
		profile string
		input   []string
	}{
		{"missing", strings.Replace(existing, "            input: [text, image]\n", "", 1), []string{"text", "image"}},
		{"null", strings.Replace(existing, "input: [text, image]", "input: null", 1), []string{"text", "image"}},
		{"explicit-image", existing, []string{"text", "image"}},
		{"explicit-text-only", strings.Replace(existing, "input: [text, image]", "input: [text]", 1), []string{"text"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			paths := dshTestHome(t)
			if err := os.WriteFile(paths.profile, []byte(tt.profile), 0600); err != nil {
				t.Fatal(err)
			}
			keyID, groupID := seedDshGroup(t)
			prepared := dshTestPrepare(t, keyID, groupID, "")
			again, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{Agent: dshAgentID, APIKeyID: keyID, DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID}, CurrentContent: &prepared.Content}, context.Background())
			if err != nil || again.Content != prepared.Content {
				t.Fatalf("model input changed on repeated preparation: %v", err)
			}
			if err := DshConfigSave(dshTestSaveRequest(t, prepared, keyID), context.Background()); err != nil {
				t.Fatal(err)
			}
			p, _, _, err := loadDshProfile(paths, nil)
			if err != nil {
				t.Fatal(err)
			}
			provider, err := p.provider(false)
			if err != nil {
				t.Fatal(err)
			}
			item, err := dshModel(provider, prepared.DSH.ModelName)
			if err != nil {
				t.Fatal(err)
			}
			input := dshGet(item, "input")
			var modalities []string
			if input == nil || input.Decode(&modalities) != nil || !slices.Equal(modalities, tt.input) {
				t.Fatalf("saved model input = %v, want %v", modalities, tt.input)
			}
			if contextWindow := dshGet(item, "contextWindow"); contextWindow == nil || contextWindow.Value != "200000" || !dshSupportsEffort(item, "high") {
				t.Fatal("auto-config changed existing model capabilities")
			}
		})
	}
}

func TestDshConflictsAndUnrelatedCredentialRefresh(t *testing.T) {
	for _, scenario := range []string{"profile", "managed-ref", "other-record"} {
		t.Run(scenario, func(t *testing.T) {
			paths := dshTestHome(t)
			keyID, groupID := seedDshGroup(t)
			prepared := dshTestPrepare(t, keyID, groupID, "")
			req := dshTestSaveRequest(t, prepared, keyID)
			switch scenario {
			case "profile":
				_ = os.WriteFile(paths.profile, []byte(dshTestProfile+"# external edit\n"), 0600)
				_ = os.Chtimes(paths.profile, time.Now(), time.Now().Add(time.Second))
			case "managed-ref":
				_ = os.WriteFile(paths.credentials, []byte(strings.Replace(dshTestCredentials, "  TOKENHUB_API_KEY:", "  OCTOPUS_DESKTOP_API_KEY: changed-outside\n  TOKENHUB_API_KEY:", 1)), 0600)
			case "other-record":
				_ = os.WriteFile(paths.credentials, []byte(strings.ReplaceAll(dshTestCredentials, "account-secret", "refreshed-account-secret")), 0600)
			}
			err := DshConfigSave(req, context.Background())
			if scenario == "other-record" {
				if err != nil {
					t.Fatal(err)
				}
				text, _ := os.ReadFile(paths.credentials)
				if !strings.Contains(string(text), "refreshed-account-secret") {
					t.Fatal("lost concurrent refresh")
				}
			} else if !errors.Is(err, ErrAgentConfigConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
}

func TestDshSecondCommitFailureRollsBackCredential(t *testing.T) {
	paths := dshTestHome(t)
	keyID, groupID := seedDshGroup(t)
	prepared := dshTestPrepare(t, keyID, groupID, "")
	err := saveDshConfig(dshTestSaveRequest(t, prepared, keyID), context.Background(), func(ctx context.Context, temporary, target string) error {
		if target == paths.profile {
			return errors.New("simulated profile replacement failure")
		}
		return renameDshFile(ctx, temporary, target)
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	for path, expected := range map[string]string{paths.credentials: dshTestCredentials, paths.profile: dshTestProfile} {
		text, _ := os.ReadFile(path)
		if string(text) != expected {
			t.Fatal("partial update remained after failure")
		}
	}
}

func TestDshLockCoordinationAndPrivateDiagnostics(t *testing.T) {
	paths := dshTestHome(t)
	lock, err := acquireDshFileLock(context.Background(), paths.credentials, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := acquireDshFileLock(ctx, paths.credentials, time.Second); err == nil {
		t.Fatal("ignored active DSH lock")
	}
	_ = os.Remove(lock)
	if err := os.WriteFile(lock, []byte("2000000000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	claimed, err := acquireDshFileLock(context.Background(), paths.credentials, time.Second)
	if err != nil {
		t.Fatal("did not take over exited holder")
	}
	_ = os.Remove(claimed)
	for _, content := range []string{"version: 1\nrefs:\n  KEY: [secret-value,\n", "version: 1\nrefs:\n  KEY: secret-value\n  KEY: other-value\n", "version: 2\nrefs: {}\n", "version: 1\nrefs:\n  KEY: 123\n", "version: 1\nrecords:\n  other/record:\n    kind: invalid-secret-value\n"} {
		_, err := parseDshCredentials(content)
		if err == nil || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("unsafe private validation: %v", err)
		}
	}
	if _, err := resolveDshPaths("../desktop"); err == nil {
		t.Fatal("accepted arbitrary profile path")
	}
	if _, err := resolveDshPaths(filepath.Join("desktop", "other")); err == nil {
		t.Fatal("accepted nested profile")
	}
}

// DSH patch layers may repeat an entry id; the later row replaces the earlier
// one. The editor must accept that shape and resolve the last row.
func TestDshDuplicatePatchEntriesUseLastWrite(t *testing.T) {
	paths := dshTestHome(t)
	if err := os.WriteFile(paths.profile, []byte(dshDuplicateProfile), 0600); err != nil {
		t.Fatal(err)
	}

	p, _, _, err := loadDshProfile(paths, nil)
	if err != nil {
		t.Fatalf("duplicate DSH patch ids should be accepted: %v", err)
	}
	if provider, err := p.provider(false); err != nil {
		t.Fatal(err)
	} else if provider != nil {
		t.Fatal("the last llm-pi-ai row has no octopus provider")
	}
	llmConfig, err := p.config("llm-pi-ai", false)
	if err != nil {
		t.Fatal(err)
	}
	providers := dshGet(llmConfig, "providers")
	if got := dshText(dshGet(dshGet(providers, "magpie"), "displayName")); got != "Magpie" {
		t.Fatalf("provider should come from the last llm-pi-ai row, got %q", got)
	}
	if dshGet(providers, "octopus") != nil {
		t.Fatal("the earlier octopus row should not be treated as active")
	}
	defaults, err := p.config("agent-default-model", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := dshText(dshGet(defaults, "provider")), "magpie"; got != want {
		t.Fatalf("default provider = %q, want %q", got, want)
	}
	if got, want := dshText(dshGet(defaults, "model")), "workbuddy/deepseek-v4.1-flash"; got != want {
		t.Fatalf("default model = %q, want %q", got, want)
	}
}

// Auto-config must edit the effective (last) patch row while keeping other
// providers from that row, instead of failing on the duplicated ids.
func TestDshAutoConfigWithDuplicatePatchEntries(t *testing.T) {
	paths := dshTestHome(t)
	if err := os.WriteFile(paths.profile, []byte(dshDuplicateProfile), 0600); err != nil {
		t.Fatal(err)
	}
	keyID, groupID := seedDshGroup(t)

	_, state, err := DshConfigList("desktop", context.Background())
	if err != nil {
		t.Fatalf("DshConfigList failed on duplicate patch ids: %v", err)
	}
	if state.CurrentProvider != "magpie" {
		t.Fatalf("current provider = %q, want magpie", state.CurrentProvider)
	}

	prepared := dshTestPrepare(t, keyID, groupID, "openai-responses")
	doc, err := parseDshYAML(prepared.Content, yaml.SequenceNode)
	if err != nil {
		t.Fatal(err)
	}
	p := &dshProfile{patch: doc, base: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode}}}}
	llmConfig, err := p.config("llm-pi-ai", false)
	if err != nil {
		t.Fatal(err)
	}
	providers := dshGet(llmConfig, "providers")
	if dshGet(providers, "octopus") == nil {
		t.Fatal("auto-config did not add the octopus provider")
	}
	if dshGet(providers, "magpie") == nil {
		t.Fatal("auto-config lost the existing magpie provider")
	}
	defaults, err := p.config("agent-default-model", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := dshText(dshGet(defaults, "provider")); got != "octopus" {
		t.Fatalf("default provider = %q, want octopus", got)
	}
	if got := dshText(dshGet(defaults, "model")); got != "public-group-alias" {
		t.Fatalf("default model = %q, want public-group-alias", got)
	}
}
