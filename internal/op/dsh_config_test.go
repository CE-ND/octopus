package op

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if prepared.DSH.API != "openai-responses" || !prepared.DSH.ReasoningReset {
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
	if strings.Contains(prepared.Content, "reasoningEffort: max") || strings.Contains(prepared.Content, "sk-auto-test") {
		t.Fatal("unsafe reasoning or inline credential")
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
