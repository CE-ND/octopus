package op

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// 实时调用拓扑的内存态跟踪：记录「哪个 agent 正在通过哪个密钥调用哪个分组」。
// 纯内存不落库，服务重启后自然清零；条目超过 topologyEntryTTL 无活动即清除。

const (
	// TopologyActiveWindow 最近一次调用落在该窗口内视为活跃（前端据此画流光实线）
	TopologyActiveWindow = 60 * time.Second
	// topologyEntryTTL 无活动条目的保留时长，超时后从注册表清除
	topologyEntryTTL = 15 * time.Minute
)

// TopologyCall 一次调用的归因信息，由 relay 在请求开始时构造
type TopologyCall struct {
	Agent      string // 调用方 agent 标识：claude-code / codex / gemini-cli / other
	APIKeyID   int
	GroupID    int
	GroupName  string
	Model      string // 请求模型名（在本系统中即分组名）
	Protocol   string // anthropic / openai-chat / openai-responses / embedding
}

type topologyEntry struct {
	agent       string
	apiKeyName  string
	groupID     int
	groupName   string
	model       string
	protocol    string

	active      int    // 在途请求数
	calls       int64  // 保留期内总调用次数
	lastSeen    int64  // 最近一次活动（开始或结束）的 unix 秒
	lastSuccess int32  // -1 未知；0 失败；1 成功（最近一次）
}

var (
	topologyMu      sync.RWMutex
	topologyEntries = map[string]*topologyEntry{}
)

func topologyEntryKey(call TopologyCall, keyName string) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%s", call.Agent, keyName, call.GroupID, call.Model)
}

// TopologyBegin 登记一次调用的开始，返回幂等的结束函数（end(success) 记录最终成败并释放占位）。
// 结束函数可安全多次调用（sync.Once），正式路径在 metrics 收敛点调用，defer 兜底防泄漏。
func TopologyBegin(call TopologyCall) func(success bool) {
	keyName := fmt.Sprintf("#%d", call.APIKeyID)
	if apiKey, err := APIKeyGet(call.APIKeyID, context.Background()); err == nil {
		keyName = apiKey.Name
	}

	now := time.Now().Unix()
	key := topologyEntryKey(call, keyName)

	topologyMu.Lock()
	entry := topologyEntries[key]
	if entry == nil {
		entry = &topologyEntry{
			agent:       call.Agent,
			apiKeyName:  keyName,
			groupID:     call.GroupID,
			groupName:   call.GroupName,
			model:       call.Model,
			protocol:    call.Protocol,
			lastSuccess: -1,
		}
		topologyEntries[key] = entry
	}
	entry.active++
	entry.calls++
	entry.lastSeen = now
	topologyMu.Unlock()

	var once sync.Once
	return func(success bool) {
		once.Do(func() {
			topologyMu.Lock()
			entry.active--
			if entry.active < 0 {
				entry.active = 0
			}
			entry.lastSeen = time.Now().Unix()
			if success {
				atomic.StoreInt32(&entry.lastSuccess, 1)
			} else {
				atomic.StoreInt32(&entry.lastSuccess, 0)
			}
			topologyMu.Unlock()
		})
	}
}

// TopologyGroupLink 同一调用方对同一分组的一条调用链路视图
type TopologyGroupLink struct {
	GroupID     int    `json:"group_id"`
	GroupName   string `json:"group_name"`
	Model       string `json:"model"`
	Protocol    string `json:"protocol"`
	Active      int    `json:"active"`
	Calls       int64  `json:"calls"`
	LastSeen    int64  `json:"last_seen"`
	LastSuccess *bool  `json:"last_success"`
}

// TopologyCaller 一个调用方（agent + 密钥）的聚合视图
type TopologyCaller struct {
	Agent      string              `json:"agent"`
	APIKeyName string              `json:"api_key_name"`
	Active     int                 `json:"active"`
	Calls      int64               `json:"calls"`
	LastSeen   int64               `json:"last_seen"`
	Groups     []TopologyGroupLink `json:"groups"`
}

// TopologySnapshot 拓扑当前状态；读取时顺带清理过期条目
type TopologySnapshot struct {
	Now     int64            `json:"now"`
	Callers []TopologyCaller `json:"callers"`
}

func TopologySnapshotGet() TopologySnapshot {
	now := time.Now().Unix()
	ttlSeconds := int64(topologyEntryTTL / time.Second)

	topologyMu.Lock()
	for key, entry := range topologyEntries {
		if entry.active <= 0 && now-entry.lastSeen > ttlSeconds {
			delete(topologyEntries, key)
		}
	}
	callers := make([]TopologyCaller, 0)
	callerIndex := map[string]int{}
	for _, entry := range topologyEntries {
		key := entry.agent + "\x00" + entry.apiKeyName
		idx, ok := callerIndex[key]
		if !ok {
			callerIndex[key] = len(callers)
			idx = len(callers)
			callers = append(callers, TopologyCaller{
				Agent:      entry.agent,
				APIKeyName: entry.apiKeyName,
				Active:     0,
				Calls:      0,
				LastSeen:   0,
				Groups:     make([]TopologyGroupLink, 0),
			})
		}
		caller := &callers[idx]
		caller.Active += entry.active
		caller.Calls += entry.calls
		if entry.lastSeen > caller.LastSeen {
			caller.LastSeen = entry.lastSeen
		}
		var lastSuccess *bool
		if entry.lastSuccess >= 0 {
			value := entry.lastSuccess == 1
			lastSuccess = &value
		}
		caller.Groups = append(caller.Groups, TopologyGroupLink{
			GroupID:     entry.groupID,
			GroupName:   entry.groupName,
			Model:       entry.model,
			Protocol:    entry.protocol,
			Active:      entry.active,
			Calls:       entry.calls,
			LastSeen:    entry.lastSeen,
			LastSuccess: lastSuccess,
		})
	}
	topologyMu.Unlock()

	for i := range callers {
		groups := callers[i].Groups
		sort.SliceStable(groups, func(a, b int) bool { return groups[a].LastSeen > groups[b].LastSeen })
	}
	sort.SliceStable(callers, func(a, b int) bool { return callers[a].LastSeen > callers[b].LastSeen })

	return TopologySnapshot{Now: now, Callers: callers}
}
