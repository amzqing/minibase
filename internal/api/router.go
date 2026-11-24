package api

import "strings"

// appRoute 解析 /api/apps/{app}[/...]，返回应用名和剩余路径段。
func appRoute(path string) (string, []string, bool) {
	if !strings.HasPrefix(path, "/api/apps/") {
		return "", nil, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/apps/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", nil, false
	}
	return parts[0], parts[1:], true
}

// storageRoute 解析 /api/storage/{action}/{app}[/{fileID}]。
func storageRoute(path string) (string, string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/api/storage/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", false
	}
	fileID := ""
	if len(parts) >= 3 {
		fileID = parts[2]
	}
	return parts[0], parts[1], fileID, true
}
