package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type packageJSON struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Description string          `json:"description"`
	Homepage    string          `json:"homepage"`
	Private     bool            `json:"private"`
	Main        string          `json:"main"`
	Module      string          `json:"module"`
	Exports     json.RawMessage `json:"exports"`
	Octopus     struct {
		DisplayName string   `json:"displayName"`
		Icon        string   `json:"icon"`
		Replaces    []string `json:"replaces"`
		Upstream    struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"upstream"`
		UI struct {
			ConfigSchema json.RawMessage `json:"configSchema"`
			Pages        []PluginUIPage  `json:"pages"`
		} `json:"ui"`
	} `json:"octopus"`
}

func (p packageJSON) displayName() string {
	if strings.TrimSpace(p.Octopus.DisplayName) != "" {
		return strings.TrimSpace(p.Octopus.DisplayName)
	}
	return p.Name
}

func readPackageJSON(dir string) (packageJSON, error) {
	var pkg packageJSON
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return pkg, fmt.Errorf("read package.json: %w", err)
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return pkg, fmt.Errorf("parse package.json: %w", err)
	}
	if strings.TrimSpace(pkg.Name) == "" {
		return pkg, errors.New("package.json has no name")
	}
	if strings.TrimSpace(pkg.Version) == "" {
		return pkg, errors.New("package.json has no version")
	}
	return pkg, nil
}

func safePluginDirName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "@", "")
	name = strings.ReplaceAll(name, "/", "__")
	name = strings.ReplaceAll(name, "\\", "__")
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" {
		name = "plugin"
	}
	return name
}

func copyDir(src, dst string) error {
	src, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	dst, err = filepath.Abs(dst)
	if err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// installPackage 把 npm 包或本地目录安装到 dataDir/<plugin-dir>，返回安装条目。
func installPackage(ctx context.Context, dataDir, spec, registry string) (InstalledPlugin, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return InstalledPlugin{}, errors.New("plugin spec is empty")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return InstalledPlugin{}, err
	}

	tmpRoot, err := os.MkdirTemp(dataDir, ".install-*")
	if err != nil {
		return InstalledPlugin{}, err
	}
	defer os.RemoveAll(tmpRoot)

	var pkg packageJSON
	var sourceCreated time.Time

	if info, statErr := os.Stat(spec); statErr == nil {
		if info.IsDir() {
			if err := copyDir(spec, tmpRoot); err != nil {
				return InstalledPlugin{}, fmt.Errorf("copy local plugin: %w", err)
			}
		} else if strings.HasSuffix(strings.ToLower(spec), ".tgz") || strings.HasSuffix(strings.ToLower(spec), ".tar.gz") {
			if err := extractNPMTarball(spec, tmpRoot); err != nil {
				return InstalledPlugin{}, err
			}
		} else {
			return InstalledPlugin{}, fmt.Errorf("plugin path is not a directory or .tgz: %s", spec)
		}
		sourceCreated = info.ModTime()
	} else {
		if err := downloadNPMPackage(ctx, spec, registry, tmpRoot); err != nil {
			return InstalledPlugin{}, err
		}
		sourceCreated = time.Now()
	}

	pkg, err = readPackageJSON(tmpRoot)
	if err != nil {
		return InstalledPlugin{}, err
	}
	dirName := safePluginDirName(pkg.Name)
	targetDir := filepath.Join(dataDir, dirName)
	backupDir := targetDir + ".old-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if _, statErr := os.Stat(targetDir); statErr == nil {
		if err := os.Rename(targetDir, backupDir); err != nil {
			return InstalledPlugin{}, fmt.Errorf("replace existing plugin: %w", err)
		}
		defer os.RemoveAll(backupDir)
	}
	if err := os.Rename(tmpRoot, targetDir); err != nil {
		return InstalledPlugin{}, fmt.Errorf("move plugin into place: %w", err)
	}
	// tmpRoot was moved; prevent deferred RemoveAll from touching targetDir.
	tmpRoot = ""
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		absDir = targetDir
	}
	return InstalledPlugin{
		ID:          pkg.Name,
		DisplayName: pkg.displayName(),
		Spec:        spec,
		Version:     pkg.Version,
		Dir:         absDir,
		Enabled:     true,
		Replaces:    pkg.Octopus.Replaces,
		InstalledAt: sourceCreated,
	}, nil
}

func sortPlugins(plugins []InstalledPlugin) {
	sort.Slice(plugins, func(i, j int) bool {
		return strings.ToLower(plugins[i].ID) < strings.ToLower(plugins[j].ID)
	})
}
