package op

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/utils/log"
)

const (
	claudeSessionScanLimit  = 200
	claudeSessionMetaValues = 64
	claudeTitleMaxLen       = 80
)

// claudeModelVariantSuffix 去掉会话记录里模型名的本地变体后缀（如 [1m]），
// 这些后缀只存在于本地记录，实际请求发送的是去掉后缀的模型名。
var claudeModelVariantSuffix = regexp.MustCompile(`\[[^\[\]]*\]$`)

func claudeHomeDir() string {
	if root := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

// discoverClaudeSessions 扫描 Claude Code 的本地会话记录：
// ~/.claude/projects/<工作区>/<会话UUID>.jsonl，文件名即会话 ID，
// 与请求头 X-Claude-Code-Session-Id / metadata.user_id 里的 session_id 一致。
func discoverClaudeSessions(knownIDs map[string]struct{}) []codexLocalSession {
	root := claudeHomeDir()
	if root == "" {
		return nil
	}
	entries, err := collectClaudeTranscripts(filepath.Join(root, "projects"))
	if err != nil {
		log.Debugw("claude_session.walk_failed", "error", err.Error())
		return nil
	}
	sessions := make([]codexLocalSession, 0, len(entries))
	for _, entry := range entries {
		sessionID := strings.TrimSuffix(entry.name, ".jsonl")
		if sessionID == "" {
			continue
		}
		if _, ok := knownIDs[sessionID]; ok {
			continue
		}
		meta := readClaudeTranscriptMeta(entry.path)
		sessions = append(sessions, codexLocalSession{
			ID:        sessionID,
			Title:     meta.Title,
			CWD:       meta.CWD,
			Model:     meta.Model,
			UpdatedAt: entry.modTime.UnixMilli(),
			Source:    "claude",
		})
	}
	return sessions
}

type claudeTranscriptEntry struct {
	path    string
	name    string
	modTime time.Time
}

func collectClaudeTranscripts(projectsDir string) ([]claudeTranscriptEntry, error) {
	entries := make([]claudeTranscriptEntry, 0)
	err := filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, claudeTranscriptEntry{path: path, name: d.Name(), modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})
	if len(entries) > claudeSessionScanLimit {
		entries = entries[:claudeSessionScanLimit]
	}
	return entries, nil
}

// claudeTranscriptMeta 是从会话 jsonl 开头若干条记录里提取的展示信息。
type claudeTranscriptMeta struct {
	Title string
	CWD   string
	Model string
}

// readClaudeTranscriptMeta 只读每个文件开头的少量记录：标题取首条真实用户
// 输入（跳过命令、系统注入与后台调用），模型取首条助手回复记录的请求模型，
// <synthetic> / haiku 是本地合成或后台小模型的记录，不代表会话模型。
func readClaudeTranscriptMeta(path string) claudeTranscriptMeta {
	file, err := os.Open(path)
	if err != nil {
		return claudeTranscriptMeta{}
	}
	defer file.Close()

	var meta claudeTranscriptMeta
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 64*1024))
	for i := 0; i < claudeSessionMetaValues; i++ {
		var line struct {
			Type    string `json:"type"`
			Summary string `json:"summary"`
			CWD     string `json:"cwd"`
			IsMeta  bool   `json:"isMeta"`
			Message *struct {
				Role    string          `json:"role"`
				Model   string          `json:"model"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := decoder.Decode(&line); err != nil {
			break
		}
		if line.CWD != "" && meta.CWD == "" {
			meta.CWD = strings.TrimPrefix(line.CWD, `\\?\`)
		}
		switch line.Type {
		case "summary":
			if meta.Title == "" && line.Summary != "" {
				meta.Title = truncateClaudeTitle(line.Summary)
			}
		case "user":
			if meta.Title == "" && !line.IsMeta && line.Message != nil && line.Message.Role == "user" {
				if text := claudeMessageText(line.Message.Content); text != "" {
					meta.Title = truncateClaudeTitle(text)
				}
			}
		case "assistant":
			if meta.Model == "" && line.Message != nil {
				meta.Model = claudeSessionModel(line.Message.Model)
			}
		}
		if meta.Title != "" && meta.CWD != "" && meta.Model != "" {
			break
		}
	}
	return meta
}

func claudeSessionModel(raw string) string {
	m := strings.TrimSpace(raw)
	if m == "" || m == "<synthetic>" || m == "haiku" {
		return ""
	}
	return strings.TrimSpace(claudeModelVariantSuffix.ReplaceAllString(m, ""))
}

func claudeMessageText(content json.RawMessage) string {
	trimmed := strings.TrimSpace(string(content))
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(content, &text); err != nil {
			return ""
		}
		return cleanClaudeTitleText(text)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type == "text" {
			if text := cleanClaudeTitleText(block.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

// cleanClaudeTitleText 过滤掉不是用户真实输入的文本：斜杠命令、命令输出、
// 系统注入等；用户消息常被 <system-reminder> 包裹，剥掉后剩下的才算。
func cleanClaudeTitleText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, prefix := range []string{"<command-name>", "<local-command", "Caveat:"} {
		if strings.HasPrefix(text, prefix) {
			return ""
		}
	}
	for {
		start := strings.Index(text, "<system-reminder>")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "</system-reminder>")
		if end < 0 {
			break
		}
		text = strings.TrimSpace(text[:start] + text[start+end+len("</system-reminder>"):])
	}
	if text == "" || strings.HasPrefix(text, "<") {
		return ""
	}
	return text
}

func truncateClaudeTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > claudeTitleMaxLen {
		return string(runes[:claudeTitleMaxLen])
	}
	return text
}
