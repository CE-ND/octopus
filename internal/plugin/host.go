package plugin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed host/host.mjs
var hostAssets embed.FS

// HostOptions 是启动 plugin-host 的参数。
type HostOptions struct {
	DataDir      string
	NodePath     string
	NodeElectron bool
}

// Host 管理一个 plugin-host 子进程。
type Host struct {
	cmd        *exec.Cmd
	baseURL    string
	token      string
	running    atomic.Bool
	mu         sync.Mutex
	logFile    *os.File
	stderrDone chan struct{}
	done       chan struct{}
	waitOnce   sync.Once
}

type hostReady struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func resolveNodeBinary(nodePath string) (string, error) {
	nodePath = strings.TrimSpace(nodePath)
	if nodePath != "" {
		if filepath.IsAbs(nodePath) || strings.ContainsAny(nodePath, `/\`) {
			if _, err := os.Stat(nodePath); err != nil {
				return "", fmt.Errorf("plugin node binary not found: %w", err)
			}
			return nodePath, nil
		}
		if path, err := exec.LookPath(nodePath); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("plugin node binary not found in PATH: %s", nodePath)
	}
	path, err := exec.LookPath("node")
	if err != nil {
		return "", errors.New("Node.js not found; set plugin.node_path or OCTOPUS_PLUGIN_NODE")
	}
	return path, nil
}

func hostToken(dataDir string) (string, error) {
	path := filepath.Join(dataDir, ".host-token")
	data, err := os.ReadFile(path)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func writeHostAssets(dataDir string) (string, error) {
	hostDir := filepath.Join(dataDir, ".host")
	if err := os.MkdirAll(hostDir, 0o755); err != nil {
		return "", err
	}
	data, err := hostAssets.ReadFile("host/host.mjs")
	if err != nil {
		return "", err
	}
	hostPath := filepath.Join(hostDir, "host.mjs")
	if err := os.WriteFile(hostPath, data, 0o600); err != nil {
		return "", err
	}
	return hostPath, nil
}

// StartHost 启动一个 plugin-host 并等待它报告监听端口。
func StartHost(ctx context.Context, opts HostOptions) (*Host, error) {
	nodePath, err := resolveNodeBinary(opts.NodePath)
	if err != nil {
		return nil, err
	}
	hostPath, err := writeHostAssets(opts.DataDir)
	if err != nil {
		return nil, err
	}
	token, err := hostToken(opts.DataDir)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(nodePath, hostPath)
	cmd.Dir = opts.DataDir
	env := os.Environ()
	env = append(env,
		"OCTOPUS_PLUGIN_DATA_DIR="+opts.DataDir,
		"OCTOPUS_PLUGIN_TOKEN="+token,
	)
	if opts.NodeElectron {
		env = append(env, "ELECTRON_RUN_AS_NODE=1")
	}
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin host: %w", err)
	}
	h := &Host{cmd: cmd, token: token, stderrDone: make(chan struct{}), done: make(chan struct{})}
	logPath := filepath.Join(opts.DataDir, "host.log")
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	h.logFile = logFile
	if logFile != nil {
		go func() {
			defer close(h.stderrDone)
			_, _ = io.Copy(logFile, stderr)
		}()
	} else {
		close(h.stderrDone)
	}
	readyCh := make(chan hostReady, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "OCTOPUS_PLUGIN_HOST_READY ") {
				var ready hostReady
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "OCTOPUS_PLUGIN_HOST_READY ")), &ready); err != nil {
					errCh <- err
					return
				}
				readyCh <- ready
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- errors.New("plugin host exited before ready")
		}
	}()
	select {
	case ready := <-readyCh:
		if ready.Token != "" {
			h.token = ready.Token
		}
		h.baseURL = fmt.Sprintf("http://127.0.0.1:%d", ready.Port)
		h.running.Store(true)
		go h.waitLoop()
		return h, nil
	case err := <-errCh:
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("plugin host handshake: %w", err)
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		return nil, errors.New("plugin host start timed out")
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	}
}

func (h *Host) Running() bool {
	return h != nil && h.running.Load()
}

func (h *Host) BaseURL() string {
	return h.baseURL
}

func (h *Host) Token() string {
	return h.token
}

func (h *Host) waitLoop() {
	h.waitOnce.Do(func() {
		_ = h.cmd.Wait()
		h.running.Store(false)
		close(h.done)
	})
}

func (h *Host) Stop() error {
	if h == nil {
		return nil
	}
	h.running.Store(false)
	if h.cmd != nil && h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
		}
	}
	if h.stderrDone != nil {
		select {
		case <-h.stderrDone:
		case <-time.After(time.Second):
		}
	}
	h.mu.Lock()
	if h.logFile != nil {
		_ = h.logFile.Close()
		h.logFile = nil
	}
	h.mu.Unlock()
	return nil
}

func (h *Host) controlClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func (h *Host) controlRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.baseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.controlClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Error.Message != "" {
			return nil, errors.New(apiErr.Error.Message)
		}
		return nil, fmt.Errorf("plugin host returned %s", resp.Status)
	}
	return data, nil
}

// Status 获取插件宿主的轻量状态。
func (h *Host) Status(ctx context.Context) ([]PluginInfo, error) {
	data, err := h.controlRequest(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return nil, err
	}
	var resp HostStatusResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.Plugins == nil {
		resp.Plugins = []PluginInfo{}
	}
	return resp.Plugins, nil
}

// Providers 获取插件 provider 与模型列表。
func (h *Host) Providers(ctx context.Context, refresh bool) ([]ProviderInfo, error) {
	path := "/providers"
	if refresh {
		path += "?refresh=1"
	}
	data, err := h.controlRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var resp ProvidersResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if resp.Providers == nil {
		resp.Providers = []ProviderInfo{}
	}
	return resp.Providers, nil
}

// AuthMethods 获取某个 provider 的登录方式。
func (h *Host) AuthMethods(ctx context.Context, pluginID, providerID string) ([]AuthMethod, error) {
	data, err := h.controlRequest(ctx, http.MethodGet, "/auth/methods?plugin_id="+urlQuery(pluginID)+"&provider_id="+urlQuery(providerID), nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Methods []AuthMethod `json:"methods"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Methods, nil
}

// StartLogin 开始一次登录。
func (h *Host) StartLogin(ctx context.Context, in AuthStartRequest) (AuthSession, error) {
	body := map[string]any{
		"plugin_id":   in.PluginID,
		"provider_id": in.ProviderID,
		"method":      in.Method,
		"api_key":     in.APIKey,
	}
	data, err := h.controlRequest(ctx, http.MethodPost, "/auth/start", body)
	if err != nil {
		return AuthSession{}, err
	}
	var resp struct {
		Status       string `json:"status"`
		URL          string `json:"url"`
		Instructions string `json:"instructions"`
		SessionID    string `json:"session_id"`
		Account      string `json:"account"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return AuthSession{}, err
	}
	return AuthSession{
		ID:           resp.SessionID,
		Status:       resp.Status,
		URL:          resp.URL,
		Instructions: resp.Instructions,
		Account:      resp.Account,
		Error:        resp.Error,
	}, nil
}

// LoginStatus 查询登录会话。
func (h *Host) LoginStatus(ctx context.Context, sessionID string) (LoginStatus, error) {
	data, err := h.controlRequest(ctx, http.MethodGet, "/auth/status?session_id="+urlQuery(sessionID), nil)
	if err != nil {
		return LoginStatus{}, err
	}
	var resp LoginStatus
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, err
	}
	return resp, nil
}

// Logout 清除 provider 的登录凭据。
func (h *Host) Logout(ctx context.Context, pluginID, providerID string) error {
	_, err := h.controlRequest(ctx, http.MethodPost, "/auth/logout", map[string]any{
		"plugin_id":   pluginID,
		"provider_id": providerID,
	})
	return err
}

func urlQuery(value string) string {
	// Small local helper avoids importing net/url in both host.go and manager.go.
	replacer := strings.NewReplacer("%", "%25", " ", "%20", "+", "%2B", "&", "%26", "=", "%3D", "?", "%3F", "#", "%23")
	return replacer.Replace(value)
}
