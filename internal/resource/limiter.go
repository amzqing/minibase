// Package resource 负责应用的资源刹车。第一版只做内存上限。
package resource

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Limiter 抽象资源限制。逻辑测试用 NoopLimiter，不依赖 cgroups。
type Limiter interface {
	// Apply 把 pid 放进受限的 cgroup，设置内存和 CPU 上限。
	Apply(pid int, key string, memoryMB int, cpuPercent int) error
	// Remove 清理 cgroup。
	Remove(key string) error
}

// NoopLimiter 什么都不做，给测试和非 Linux 环境用。
type NoopLimiter struct{}

func (NoopLimiter) Apply(int, string, int, int) error { return nil }
func (NoopLimiter) Remove(string) error               { return nil }

// CgroupV2Limiter 通过写 cgroups v2 的 memory.max 限制应用内存。
type CgroupV2Limiter struct {
	Root string
}

// NewLimiter 按平台选择实现。非 Linux 或 cgroups v2 不存在时退化为 NoopLimiter。
func NewLimiter(root string, memoryMB int) Limiter {
	if runtime.GOOS != "linux" || root == "" {
		return NoopLimiter{}
	}
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		log.Printf("[resource] 未检测到 cgroups v2 (%s)，内存限制未启用", root)
		return NoopLimiter{}
	}
	c := &CgroupV2Limiter{Root: root}
	if err := c.prepare(); err != nil {
		log.Printf("[resource] cgroups v2 委派准备失败，内存限制未启用: %v", err)
		return NoopLimiter{}
	}
	return c
}

// prepare 建立 core / apps 两个子 cgroup：
// Core 进 core，应用进 apps。apps 必须为空才能启用 memory 控制器，
// 这样应用子 cgroup 才有可写的 memory.max。
func (c *CgroupV2Limiter) prepare() error {
	coreDir := filepath.Join(c.Root, "core")
	appsDir := filepath.Join(c.Root, "apps")
	if err := os.MkdirAll(coreDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(coreDir, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(appsDir, 0o755); err != nil {
		return err
	}
	// 根层级同时启用 memory 和 cpu。必须用 "+memory +cpu" 一次写，
	// 分两次写时第二次会因 cgroup v2 的 atomic 规则失败。
	if err := os.WriteFile(filepath.Join(c.Root, "cgroup.subtree_control"), []byte("+memory +cpu"), 0o644); err != nil {
		log.Printf("[resource] 根层级启用 cpu 失败（CPU 限额可能不可用）: %v", err)
		if err2 := os.WriteFile(filepath.Join(c.Root, "cgroup.subtree_control"), []byte("+memory"), 0o644); err2 != nil {
			return err2
		}
	}
	// apps 层同样一次写入两个控制器
	if err := os.WriteFile(filepath.Join(appsDir, "cgroup.subtree_control"), []byte("+memory +cpu"), 0o644); err != nil {
		log.Printf("[resource] apps 层启用 cpu 失败（CPU 限额可能不可用）: %v", err)
		if err2 := os.WriteFile(filepath.Join(appsDir, "cgroup.subtree_control"), []byte("+memory"), 0o644); err2 != nil {
			return err2
		}
	}
	return nil
}

func safeKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

func (c *CgroupV2Limiter) dir(key string) string {
	return filepath.Join(c.Root, "apps", safeKey(key))
}

func (c *CgroupV2Limiter) Apply(pid int, key string, memoryMB int, cpuPercent int) error {
	dir := c.dir(key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建 cgroup 目录失败: %w", err)
	}
	mem := strconv.Itoa(memoryMB * 1024 * 1024)
	if err := os.WriteFile(filepath.Join(dir, "memory.max"), []byte(mem), 0o644); err != nil {
		return fmt.Errorf("写 memory.max 失败: %w", err)
	}
	// cpu.max 格式: "配额 周期"，如 50000 100000 表示 50% CPU
	if cpuPercent > 0 {
		quota := cpuPercent * 1000
		cpuMax := fmt.Sprintf("%d 100000", quota)
		if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(cpuMax), 0o644); err != nil {
			log.Printf("[resource] 写 cpu.max 失败（CPU 限制未生效）: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("把进程加入 cgroup 失败: %w", err)
	}
	return nil
}

func (c *CgroupV2Limiter) Remove(key string) error {
	return os.RemoveAll(c.dir(key))
}
