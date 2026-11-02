package op

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledAgentsDeepSeekHarness(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		installed bool
	}{
		{name: "existing data directory", kind: "directory", installed: true},
		{name: "missing data directory", kind: "missing", installed: false},
		{name: "regular file is not an installation", kind: "file", installed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "dsh-data")
			switch tt.kind {
			case "directory":
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(root, []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("DSH_HOME", root)

			// 安装结果有 60 秒缓存；每个场景单独扫描，并在结束后恢复原缓存。
			installedAgentsMu.Lock()
			previousCache := installedAgentsCache
			previousCachedAt := installedAgentsCachedAt
			installedAgentsCache = nil
			installedAgentsMu.Unlock()
			t.Cleanup(func() {
				installedAgentsMu.Lock()
				installedAgentsCache = previousCache
				installedAgentsCachedAt = previousCachedAt
				installedAgentsMu.Unlock()
			})

			for _, agent := range InstalledAgentsGet() {
				if agent.Agent != "deepseek-harness" {
					continue
				}
				if agent.Name != "DeepSeek Harness" {
					t.Fatalf("unexpected display name: %q", agent.Name)
				}
				if agent.Installed != tt.installed {
					t.Fatalf("installed = %v, want %v", agent.Installed, tt.installed)
				}
				if got := AgentDisplayName(agent.Agent); got != agent.Name {
					t.Fatalf("AgentDisplayName = %q, want %q", got, agent.Name)
				}
				return
			}
			t.Fatal("DeepSeek Harness is missing from the installation registry")
		})
	}
}
