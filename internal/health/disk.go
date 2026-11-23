// Package health 提供磁盘空间检查与告警。
package health

import (
	"fmt"
	"log"
	"syscall"
)

// CheckDisk 检查路径所在文件系统的使用率，超过告警阈值时记日志。
// 返回 (使用率百分比, 是否超过强制阈值, 错误)。
func CheckDisk(path string, warnPercent, forcePercent int) (int, bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false, err
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	if total == 0 {
		return 0, false, fmt.Errorf("文件系统总空间为 0")
	}
	usedPercent := int((total - free) * 100 / total)
	if usedPercent >= warnPercent {
		log.Printf("[health] 磁盘告警: %s 使用率 %d%% (告警阈值 %d%%)", path, usedPercent, warnPercent)
	}
	return usedPercent, usedPercent >= forcePercent, nil
}
