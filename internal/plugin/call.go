package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// Call 将页面调用交给对应插件，宿主只在插件通知变更时同步渠道描述。
func (m *Manager) Call(ctx context.Context, request CallRequest) (CallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.host == nil || !m.host.Running() {
		return CallResult{}, errors.New("plugin host is not running")
	}
	data, err := m.host.controlRequest(ctx, http.MethodPost, "/call", request)
	if err != nil {
		return CallResult{}, err
	}
	var result CallResult
	if err := json.Unmarshal(data, &result); err != nil {
		return CallResult{}, err
	}
	if result.Refresh {
		if err := m.syncChannelsLocked(ctx); err != nil {
			return result, err
		}
	}
	return result, nil
}
