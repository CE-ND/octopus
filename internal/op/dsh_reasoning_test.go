package op

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestDshReasoningLevelsPrepareSaveAndReload(t *testing.T) {
	for _, api := range []string{"openai-completions", "openai-responses", "anthropic-messages"} {
		for _, effort := range []string{"default", "off", "low", "high", "max"} {
			t.Run(api+"/"+effort, func(t *testing.T) {
				paths := dshTestHome(t)
				keyID, groupID := seedDshGroup(t)
				req := model.AgentAutoConfigRequest{
					Agent: dshAgentID, APIKeyID: keyID,
					DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID, API: api, ReasoningEffort: &effort},
				}
				prepared, err := DshAutoConfigPrepare(req, context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if prepared.DSH.ReasoningEffort != effort || prepared.DSH.ReasoningReset {
					t.Fatalf("selection was not returned: %+v", prepared.DSH)
				}
				p, _, _, err := loadDshProfile(paths, &prepared.Content)
				if err != nil {
					t.Fatal(err)
				}
				provider, _ := p.provider(false)
				item, _ := dshModel(provider, prepared.DSH.ModelName)
				efforts := dshGet(item, "reasoningEfforts")
				if off := dshGet(efforts, "off"); off == nil || off.Tag != "!!null" {
					t.Fatal("Off must use DSH's null wire mapping")
				}
				for _, level := range []string{"low", "high", "max"} {
					if dshText(dshGet(efforts, level)) != level {
						t.Fatalf("missing reasoning level %s", level)
					}
				}
				defaults, _ := p.config("agent-default-model", false)
				if effort == "default" {
					if dshGet(defaults, "reasoningEffort") != nil || dshGet(efforts, "default") != nil {
						t.Fatal("Default must omit the selection, not become a model effort")
					}
				} else if dshText(dshGet(defaults, "reasoningEffort")) != effort {
					t.Fatal("default-model selection was not written")
				}
				req.CurrentContent = &prepared.Content
				again, err := DshAutoConfigPrepare(req, context.Background())
				if err != nil || again.Content != prepared.Content {
					t.Fatalf("repeated preparation changed reasoning: %v", err)
				}
				if err := DshConfigSave(dshTestSaveRequest(t, prepared, keyID), context.Background()); err != nil {
					t.Fatal(err)
				}
				_, state, err := DshConfigList("desktop", context.Background())
				if err != nil || state.ReasoningEffort != effort {
					t.Fatalf("saved selection did not round-trip: state=%+v, error=%v", state, err)
				}
			})
		}
	}
}

func TestDshReasoningPreservesCustomMappingsAndProviderDefault(t *testing.T) {
	paths := dshTestHome(t)
	profile := strings.Replace(dshTestProfile, "- id: existing", "- id: public-group-alias", 1)
	profile = strings.Replace(profile, "              high: high", "              off: none\n              low: minimal\n              high: high\n              max: xhigh", 1)
	profile = strings.Replace(profile, "        api: openai-responses", "        api: openai-responses\n        reasoning: high", 1)
	if err := os.WriteFile(paths.profile, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	keyID, groupID := seedDshGroup(t)
	for _, effort := range []string{"max", "default"} {
		prepared, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{
			Agent: dshAgentID, APIKeyID: keyID,
			DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID, ReasoningEffort: &effort},
		}, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		p, _, _, err := loadDshProfile(paths, &prepared.Content)
		if err != nil {
			t.Fatal(err)
		}
		provider, _ := p.provider(false)
		item, _ := dshModel(provider, prepared.DSH.ModelName)
		if dshText(dshGet(provider, "reasoning")) != "high" || dshGet(item, "contextWindow").Value != "200000" {
			t.Fatal("provider default or existing model metadata was changed")
		}
		for level, wireValue := range map[string]string{"off": "none", "low": "minimal", "high": "high", "max": "xhigh"} {
			if dshText(dshGet(dshGet(item, "reasoningEfforts"), level)) != wireValue {
				t.Fatalf("custom %s mapping was overwritten", level)
			}
		}
		if prepared.DSH.ReasoningEffort != effort {
			t.Fatal("Default must remain distinct from the provider's high default")
		}
	}
}

func TestDshReasoningPreservesExplicitRestrictions(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "high-only", true: "disabled"}[disabled], func(t *testing.T) {
			paths := dshTestHome(t)
			profile := strings.Replace(dshTestProfile, "- id: existing", "- id: public-group-alias", 1)
			if disabled {
				profile = strings.Replace(profile, "reasoningEfforts:\n              high: high", "reasoningEfforts: false", 1)
			}
			if err := os.WriteFile(paths.profile, []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			keyID, groupID := seedDshGroup(t)
			prepared := dshTestPrepare(t, keyID, groupID, "")
			if !prepared.DSH.ReasoningReset || prepared.DSH.ReasoningEffort != "default" {
				t.Fatal("an incompatible inherited effort must still be cleared")
			}
			for _, effort := range []string{"max", "invalid", ""} {
				_, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{
					Agent: dshAgentID, APIKeyID: keyID,
					DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID, ReasoningEffort: &effort},
				}, context.Background())
				if err == nil {
					t.Fatalf("accepted unsupported selection %q", effort)
				}
			}
			p, _, _, err := loadDshProfile(paths, &prepared.Content)
			if err != nil {
				t.Fatal(err)
			}
			provider, _ := p.provider(false)
			item, _ := dshModel(provider, prepared.DSH.ModelName)
			efforts := dshGet(item, "reasoningEfforts")
			if disabled {
				if efforts.Tag != "!!bool" || efforts.Value != "false" {
					t.Fatal("explicitly disabled reasoning was enabled")
				}
			} else if len(efforts.Content) != 2 || dshText(dshGet(efforts, "high")) != "high" {
				t.Fatal("explicit capability restrictions were changed")
			}
			current, _ := os.ReadFile(paths.profile)
			if string(current) != profile {
				t.Fatal("preparation changed the saved profile")
			}
		})
	}
}

func TestDshReasoningPreservesManualDraftWithoutSelection(t *testing.T) {
	dshTestHome(t)
	keyID, groupID := seedDshGroup(t)
	prepared := dshTestPrepare(t, keyID, groupID, "")
	manual := strings.Replace(prepared.Content, "reasoningEffort: max", "reasoningEffort: low", 1)
	if manual == prepared.Content {
		t.Fatal("fixture has no default reasoning level")
	}
	again, err := DshAutoConfigPrepare(model.AgentAutoConfigRequest{
		Agent: dshAgentID, APIKeyID: keyID, CurrentContent: &manual,
		DSH: &model.DshAutoConfigOptions{ModelGroupID: groupID},
	}, context.Background())
	if err != nil || again.DSH.ReasoningEffort != "low" || again.Content != manual {
		t.Fatalf("manual reasoning edit was not preserved: %v", err)
	}
}
