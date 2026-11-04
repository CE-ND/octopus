package plugin

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type npmDist struct {
	Tarball string `json:"tarball"`
	Shasum  string `json:"shasum"`
}

type npmVersion struct {
	Name    string  `json:"name"`
	Version string  `json:"version"`
	Dist    npmDist `json:"dist"`
}

func splitNPMSpec(spec string) (name, version string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	start := 0
	if strings.HasPrefix(spec, "@") {
		start = 1
	}
	if i := strings.LastIndex(spec[start:], "@"); i >= 0 {
		i += start
		if i > 0 {
			return spec[:i], spec[i+1:]
		}
	}
	return spec, ""
}

func npmRegistry(registry string) string {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		registry = "https://registry.npmjs.org"
	}
	return strings.TrimRight(registry, "/")
}

func downloadNPMPackage(ctx context.Context, spec, registry, dst string) error {
	name, version := splitNPMSpec(spec)
	if name == "" {
		return fmt.Errorf("invalid npm spec: %s", spec)
	}
	if version == "" {
		version = "latest"
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	metaURL := npmRegistry(registry) + "/" + url.PathEscape(name) + "/" + url.PathEscape(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch npm metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("npm registry returned %s for %s", resp.Status, metaURL)
	}
	var meta npmVersion
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&meta); err != nil {
		return fmt.Errorf("decode npm metadata: %w", err)
	}
	if meta.Dist.Tarball == "" {
		return fmt.Errorf("npm package %s has no tarball", spec)
	}

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, meta.Dist.Tarball, nil)
	if err != nil {
		return err
	}
	dlResp, err := client.Do(dlReq)
	if err != nil {
		return fmt.Errorf("download npm tarball: %w", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("npm tarball returned %s", dlResp.Status)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(dst), ".npm-*.tgz")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	hasher := sha1.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), io.LimitReader(dlResp.Body, 256<<20)); err != nil {
		tmpFile.Close()
		return fmt.Errorf("save npm tarball: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if meta.Dist.Shasum != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, meta.Dist.Shasum) {
			return fmt.Errorf("npm tarball checksum mismatch: got %s want %s", got, meta.Dist.Shasum)
		}
	}
	if err := extractNPMTarball(tmpPath, dst); err != nil {
		return err
	}
	return nil
}

func extractNPMTarball(tarballPath, dst string) error {
	f, err := os.Open(tarballPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open npm tarball: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read npm tarball: %w", err)
		}
		name := filepath.ToSlash(hdr.Name)
		// npm tarballs wrap everything in package/.
		if strings.HasPrefix(name, "package/") {
			name = strings.TrimPrefix(name, "package/")
		}
		name = strings.TrimPrefix(name, "/")
		if name == "" || name == "." {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("unsafe path in npm tarball: %s", hdr.Name)
		}
		target := filepath.Join(dst, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			total += hdr.Size
			if total > 256<<20 {
				return fmt.Errorf("npm tarball exceeds size limit")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(hdr.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Plugin packages do not need symlinks; skip them for safety.
			continue
		}
	}
	return nil
}
