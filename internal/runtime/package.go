// Package runtime 负责项目包校验/解压，以及按 package.json 启动 Node 应用。
package runtime

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PackageJSON 只取我们关心的字段。
type PackageJSON struct {
	Name    string `json:"name"`
	Main    string `json:"main"`
	Scripts struct {
		Start string `json:"start"`
	} `json:"scripts"`
}

// StartCommand 决定应用的启动命令（shell 形式）。
// 规则：优先 scripts.start；没有就用 main；都没有返回明确错误。
func (p *PackageJSON) StartCommand() (string, error) {
	if cmd := strings.TrimSpace(p.Scripts.Start); cmd != "" {
		return cmd, nil
	}
	if m := strings.TrimSpace(p.Main); m != "" {
		return "node " + m, nil
	}
	return "", fmt.Errorf("package.json 里既没有 scripts.start 也没有 main，无法确定启动命令")
}

// InspectZip 读取 zip 里的 package.json，并校验模式要求。
// mode: "a" 要求包里有 node_modules；"b" 只要求 package.json。
// 返回 package.json 内容、包内根前缀（便于解压时剥掉一层目录）。
func InspectZip(zipPath, mode string) (*PackageJSON, string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, "", fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer zr.Close()

	var pkgFile *zip.File
	hasNodeModules := false
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if name == "package.json" || strings.HasSuffix(name, "/package.json") && strings.Count(name, "/") == 1 {
			if pkgFile == nil || len(name) < len(filepath.ToSlash(pkgFile.Name)) {
				pkgFile = f
			}
		}
		if strings.HasPrefix(name, "node_modules/") || strings.Contains(name, "/node_modules/") {
			hasNodeModules = true
		}
	}
	if pkgFile == nil {
		return nil, "", fmt.Errorf("zip 里缺少 package.json")
	}
	if mode == "a" && !hasNodeModules {
		return nil, "", fmt.Errorf("模式 a 要求包里包含 node_modules，当前包里没有")
	}
	if mode != "a" && mode != "b" {
		return nil, "", fmt.Errorf("mode 只能是 a 或 b，收到 %q", mode)
	}

	rc, err := pkgFile.Open()
	if err != nil {
		return nil, "", fmt.Errorf("读取 package.json 失败: %w", err)
	}
	defer rc.Close()
	var pkg PackageJSON
	if err := json.NewDecoder(rc).Decode(&pkg); err != nil {
		return nil, "", fmt.Errorf("解析 package.json 失败: %w", err)
	}

	base := ""
	n := filepath.ToSlash(pkgFile.Name)
	if i := strings.LastIndex(n, "/"); i >= 0 {
		base = n[:i+1]
	}
	return &pkg, base, nil
}

// ExtractZip 安全解压到目标目录。
// 防 zip-slip：拒绝绝对路径、.. 路径、符号链接；控制解压总大小。
func ExtractZip(zipPath, base, dest string, maxUnpackedBytes int64) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("建目标目录失败: %w", err)
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("打开 zip 失败: %w", err)
	}
	defer zr.Close()

	var total int64
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if !strings.HasPrefix(name, base) {
			continue
		}
		rel := strings.TrimPrefix(name, base)
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue
		}
		if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
			return fmt.Errorf("zip 含非法绝对路径: %s", f.Name)
		}
		clean := filepath.Clean(filepath.FromSlash(rel))
		if clean == "." || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("zip 含非法上跳路径: %s", f.Name)
		}
		// 拒绝符号链接。
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("zip 含符号链接，第一版不支持: %s", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if maxUnpackedBytes > 0 && total > maxUnpackedBytes {
			return fmt.Errorf("解压后总大小超过上限 %d MB", maxUnpackedBytes/1024/1024)
		}
		target := filepath.Join(destAbs, clean)
		if !strings.HasPrefix(target, destAbs+string(os.PathSeparator)) {
			return fmt.Errorf("zip 路径越界: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("建目录失败: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("建父目录失败: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("读取 zip 项失败: %w", err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, f.Mode().Perm()|0o600)
		if err != nil {
			rc.Close()
			return fmt.Errorf("写文件失败: %w", err)
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return fmt.Errorf("解压文件失败: %w", copyErr)
		}
	}
	return nil
}
