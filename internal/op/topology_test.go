package op

import (
	"testing"
	"time"
)

func topologyTestCall(agent string, groupID int) TopologyCall {
	return TopologyCall{
		Agent:     agent,
		APIKeyID:  1,
		GroupID:   groupID,
		GroupName: "group-a",
		Model:     "gpt-test",
		Protocol:  "openai-responses",
	}
}

func resetTopologyRegistry() {
	topologyMu.Lock()
	topologyEntries = map[string]*topologyEntry{}
	topologyMu.Unlock()
}

func TestTopologyBeginEndLifecycle(t *testing.T) {
	resetTopologyRegistry()

	end := TopologyBegin(topologyTestCall("codex", 1))

	snapshot := TopologySnapshotGet()
	if len(snapshot.Callers) != 1 {
		t.Fatalf("expected 1 caller, got %d", len(snapshot.Callers))
	}
	caller := snapshot.Callers[0]
	if caller.Agent != "codex" || caller.Active != 1 || caller.Calls != 1 {
		t.Fatalf("unexpected caller after begin: %+v", caller)
	}
	if caller.APIKeyName != "#1" {
		t.Fatalf("expected fallback key name #1 without DB, got %q", caller.APIKeyName)
	}

	end(true)

	snapshot = TopologySnapshotGet()
	if snapshot.Callers[0].Active != 0 {
		t.Fatalf("expected active 0 after end, got %d", snapshot.Callers[0].Active)
	}
	if snapshot.Callers[0].Groups[0].LastSuccess == nil || !*snapshot.Callers[0].Groups[0].LastSuccess {
		t.Fatalf("expected last_success true after end(true)")
	}
}

func TestTopologyEndIdempotent(t *testing.T) {
	resetTopologyRegistry()

	end := TopologyBegin(topologyTestCall("claude-code", 2))
	end(true)
	end(false)
	end(true)

	snapshot := TopologySnapshotGet()
	caller := snapshot.Callers[0]
	if caller.Active != 0 {
		t.Fatalf("expected active 0 after repeated ends, got %d", caller.Active)
	}
	if caller.Calls != 1 {
		t.Fatalf("expected calls 1, got %d", caller.Calls)
	}
	if caller.Groups[0].LastSuccess == nil || !*caller.Groups[0].LastSuccess {
		t.Fatalf("expected first end(true) to win, got last_success=%v", caller.Groups[0].LastSuccess)
	}
}

func TestTopologyMultipleCallersAggregated(t *testing.T) {
	resetTopologyRegistry()

	endA := TopologyBegin(topologyTestCall("codex", 1))
	endB := TopologyBegin(topologyTestCall("codex", 2))
	endC := TopologyBegin(topologyTestCall("claude-code", 1))
	defer endC(false)
	defer endB(false)
	endA(false)
	endB(true)

	snapshot := TopologySnapshotGet()
	if len(snapshot.Callers) != 2 {
		t.Fatalf("expected 2 callers, got %d", len(snapshot.Callers))
	}
	groupLinks := map[string]int{}
	for _, caller := range snapshot.Callers {
		groupLinks[caller.Agent] = len(caller.Groups)
	}
	if groupLinks["codex"] != 2 {
		t.Fatalf("expected codex caller to have 2 group links (two groups), got %d", groupLinks["codex"])
	}
	if groupLinks["claude-code"] != 1 {
		t.Fatalf("expected claude-code caller to have 1 group link, got %d", groupLinks["claude-code"])
	}
}

func TestTopologyExpiredEntriesPruned(t *testing.T) {
	resetTopologyRegistry()

	_ = TopologyBegin(topologyTestCall("codex", 1))
	stale := TopologyBegin(topologyTestCall("gemini-cli", 2))
	stale(false)

	// 把 gemini 条目的 lastSeen 拨到 TTL 之外
	topologyMu.Lock()
	for _, entry := range topologyEntries {
		if entry.agent == "gemini-cli" {
			entry.lastSeen = time.Now().Unix() - int64(topologyEntryTTL/time.Second) - 10
		}
	}
	topologyMu.Unlock()

	snapshot := TopologySnapshotGet()
	for _, caller := range snapshot.Callers {
		if caller.Agent == "gemini-cli" {
			t.Fatalf("expected gemini-cli entry pruned after TTL")
		}
	}
	if len(snapshot.Callers) != 1 || snapshot.Callers[0].Agent != "codex" {
		t.Fatalf("expected only codex caller to remain, got %+v", snapshot.Callers)
	}
}

func TestTopologySubscribeReceivesBeginAndEnd(t *testing.T) {
	resetTopologyRegistry()
	events := TopologySubscribe()
	defer TopologyUnsubscribe(events)

	end := TopologyBegin(topologyTestCall("codex", 1))
	select {
	case snapshot := <-events:
		if len(snapshot.Callers) != 1 || snapshot.Callers[0].Active != 1 {
			t.Fatalf("unexpected begin snapshot: %+v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for begin event")
	}

	end(true)
	select {
	case snapshot := <-events:
		if len(snapshot.Callers) != 1 || snapshot.Callers[0].Active != 0 {
			t.Fatalf("unexpected end snapshot: %+v", snapshot)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for end event")
	}
}

func TestAgentDisplayName(t *testing.T) {
	if AgentDisplayName("claude-code") != "Claude Code" {
		t.Fatalf("unexpected display name: %s", AgentDisplayName("claude-code"))
	}
	if AgentDisplayName("unknown-agent") != "unknown-agent" {
		t.Fatalf("expected fallback to raw id, got %s", AgentDisplayName("unknown-agent"))
	}
}
