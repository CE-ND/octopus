package op

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dshFileSnapshot struct {
	content    []byte
	exists     bool
	modifiedAt int64
	mode       os.FileMode
}
type dshFileUpdate struct {
	path     string
	content  []byte
	previous dshFileSnapshot
}
type dshFileWriter func(context.Context, string, string) error

func readDshFile(path string, sensitive bool) (dshFileSnapshot, error) {
	file := dshFileSnapshot{mode: 0600}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return file, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return file, fmt.Errorf("DSH 配置文件无法读取或不是普通文件")
	}
	if info.Size() > 2<<20 {
		return file, fmt.Errorf("DSH 配置文件过大")
	}
	if sensitive && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return file, fmt.Errorf("DSH 凭据文件需要仅属主可读写的权限（0600）")
	}
	input, err := os.Open(path)
	if err != nil {
		return file, fmt.Errorf("DSH 配置文件无法读取")
	}
	defer input.Close()
	content, err := io.ReadAll(io.LimitReader(input, (2<<20)+1))
	if err != nil || len(content) > 2<<20 {
		return file, fmt.Errorf("DSH 配置文件无法读取或过大")
	}
	file.content, file.exists, file.modifiedAt, file.mode = content, true, info.ModTime().UnixMilli(), info.Mode().Perm()
	return file, nil
}

func withDshFileLocks(ctx context.Context, paths []string, operation func() error) error {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	locked := []string{}
	defer func() {
		for i := len(locked) - 1; i >= 0; i-- {
			_ = os.Remove(locked[i])
		}
	}()
	for _, path := range paths {
		lock, err := acquireDshFileLock(ctx, path, 2*time.Second)
		if err != nil {
			return err
		}
		locked = append(locked, lock)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation()
}

// 与 DSH withFileLock 相同的 wx 文件和 PID 记录，不能按文件年龄接管活锁。
func acquireDshFileLock(ctx context.Context, path string, wait time.Duration) (string, error) {
	lockPath := path + ".lock"
	deadline := time.Now().Add(wait)
	delay := 20 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, writeErr := fmt.Fprintf(file, "%d\n", os.Getpid())
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = os.Remove(lockPath)
				return "", fmt.Errorf("DSH 写锁无法建立")
			}
			return lockPath, nil
		}
		if !os.IsExist(err) && !os.IsPermission(err) {
			return "", fmt.Errorf("DSH 配置目录无法写入")
		}
		if takeOverDshLock(lockPath) {
			continue
		}
		if time.Now().After(deadline) {
			return "", ErrDshConfigBusy
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

func dshExitedRecord(record []byte) bool {
	if len(record) < 2 || record[len(record)-1] != '\n' {
		return false
	}
	text := string(record[:len(record)-1])
	pid, err := strconv.Atoi(text)
	if err != nil || pid <= 0 || strconv.Itoa(pid) != text || pid == os.Getpid() {
		return false
	}
	return dshLockHolderExited(pid)
}

func takeOverDshLock(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return false
	}
	record, err := os.ReadFile(path)
	if err != nil || !dshExitedRecord(record) {
		return false
	}
	hash := sha256.Sum256(record)
	claim := path + ".takeover-" + hex.EncodeToString(hash[:])[:16]
	file, err := os.OpenFile(claim, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Close()
	defer os.Remove(claim)
	if err != nil {
		return false
	}
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, record) || !dshExitedRecord(current) {
		return false
	}
	return os.Remove(path) == nil
}

func stageDshFile(path string, content []byte, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".octopus-dsh-*")
	if err != nil {
		return "", fmt.Errorf("DSH 配置临时文件无法创建")
	}
	temporary := file.Name()
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(content)
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return "", fmt.Errorf("DSH 配置临时文件无法写入")
	}
	return temporary, nil
}

func renameDshFile(ctx context.Context, temporary, target string) error {
	delay := 10 * time.Millisecond
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := os.Rename(temporary, target)
		if err == nil {
			return nil
		}
		if attempt >= 5 || !dshRetryableReplace(err) {
			return fmt.Errorf("DSH 配置文件无法替换，请检查是否被占用")
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
}

func writeDshFileAtomic(ctx context.Context, path string, content []byte, mode os.FileMode) error {
	temporary, err := stageDshFile(path, content, mode)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	return renameDshFile(ctx, temporary, path)
}

func commitDshFiles(ctx context.Context, updates []dshFileUpdate, writer dshFileWriter) error {
	staged := make([]string, len(updates))
	defer func() {
		for _, path := range staged {
			if path != "" {
				_ = os.Remove(path)
			}
		}
	}()
	// 所有内容先落到同级临时文件，再进行任何正式文件替换。
	for i, update := range updates {
		path, err := stageDshFile(update.path, update.content, 0600)
		if err != nil {
			return err
		}
		staged[i] = path
		if update.previous.exists {
			if err := writeDshFileAtomic(ctx, update.path+".bak", update.previous.content, 0600); err != nil {
				return err
			}
		}
	}
	committed := 0
	var failure error
	for i, update := range updates {
		current, err := readDshFile(update.path, strings.HasSuffix(update.path, ".credentials.yaml"))
		if err != nil {
			failure = err
			break
		}
		if current.exists != update.previous.exists || !bytes.Equal(current.content, update.previous.content) {
			failure = fmt.Errorf("%w: DSH 文件已在保存期间改变", ErrAgentConfigConflict)
			break
		}
		if err = writer(ctx, staged[i], update.path); err != nil {
			failure = err
			break
		}
		committed++
	}
	if failure == nil {
		return nil
	}
	// 回滚使用独立的有界上下文，调用取消也不能留下静默的部分更新。
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	for i := committed - 1; i >= 0; i-- {
		update := updates[i]
		current, err := readDshFile(update.path, strings.HasSuffix(update.path, ".credentials.yaml"))
		if err != nil || !bytes.Equal(current.content, update.content) {
			return fmt.Errorf("DSH 保存失败且文件已改变，无法自动回滚，请检查 .bak 备份")
		}
		if update.previous.exists {
			err = writeDshFileAtomic(rollbackCtx, update.path, update.previous.content, update.previous.mode)
		} else {
			err = os.Remove(update.path)
		}
		if err != nil {
			return fmt.Errorf("DSH 保存失败且回滚未完成，请检查 .bak 备份")
		}
	}
	return failure
}
