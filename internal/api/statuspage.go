package api

import (
	"fmt"
	"net/http"
	"strings"

	"minibase/internal/deploy"
)

// statusPageHTML 极简状态页：纯 HTML + 少量 JS，数据从现有 API 读。
const statusPageHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>MiniBase 状态</title>
<style>
body{font-family:system-ui,sans-serif;margin:2rem;max-width:900px;color:#222}
h1{font-size:1.4rem} table{border-collapse:collapse;width:100%;margin:1rem 0}
th,td{border:1px solid #ddd;padding:.5rem;text-align:left} th{background:#f5f5f5}
pre{background:#f5f5f5;padding:.8rem;overflow:auto} input{padding:.4rem;margin-right:.5rem}
button{padding:.4rem .8rem;cursor:pointer} .ok{color:green} .bad{color:red}
</style>
</head>
<body>
<h1>MiniBase 状态页</h1>
<p>输入管理员 Token 后查看。<a href="https://github.com" target="_blank"></a></p>
<p><input type="password" id="token" placeholder="管理员 Token" size="40">
<button onclick="load()">刷新</button></p>
<div id="msg"></div>
<h2>应用</h2>
<table id="apps"><tr><th>应用</th><th>状态</th><th>版本</th><th>端口</th><th>访问</th></tr></table>
<h2>应用日志（点应用查看）</h2>
<pre id="applog">点击应用行的"日志"链接</pre>
<h2>最近审计</h2>
<pre id="audit"></pre>
<script>
function api(path){return fetch(path,{headers:{Authorization:'Bearer '+document.getElementById('token').value}}).then(r=>{if(!r.ok)throw new Error('HTTP '+r.status);return r.json()})}
function showLog(a){api('/api/apps/'+a+'/applog').then(d=>document.getElementById('applog').textContent=d.logs||'(空)').catch(e=>document.getElementById('applog').textContent='加载失败')}
function load(){
  document.getElementById('msg').textContent='';
  api('/api/apps-list').then(list=>{
    const t=document.getElementById('apps');
    t.innerHTML='<tr><th>应用</th><th>状态</th><th>版本</th><th>端口</th><th>访问</th></tr>';
    list.forEach(a=>{
      const tr=document.createElement('tr');
      tr.innerHTML='<td>'+a.app_name+'</td><td>'+a.status+'</td><td>'+a.current_version+'</td><td>'+a.port+'</td><td><a href="'+a.access_url+'">打开</a> <a href="#" onclick="showLog(\''+a.app_name+'\');return false">日志</a></td>';
      t.appendChild(tr);
    });
  }).catch(e=>document.getElementById('msg').textContent='加载失败: '+e.message);
  api('/api/logs/audit?limit=20').then(logs=>{
    document.getElementById('audit').textContent=logs.map(l=>new Date(l.created_at*1000).toLocaleString()+' ['+l.event+'] '+l.reason).join('\n');
  }).catch(()=>{});
}
</script>
<!--CORE_VERSION-->
</body>
</html>`

// handleStatusPage GET /status — 浏览器状态页，免 Token，页内输入 Token。
func (s *Server) handleStatusPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.Replace(statusPageHTML, "<h1>MiniBase 状态页</h1>",
		`<h1>MiniBase 状态页 <small id="core-version"></small></h1>`, 1)
	page = strings.Replace(page, "<!--CORE_VERSION-->",
		`<script>document.getElementById("core-version").textContent="`+deploy.CoreVersion+`"</script>`, 1)
	_, _ = fmt.Fprint(w, page)
}

// handleAppsList GET /api/apps — 应用列表（需 Token）。
func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	apps, err := s.store.ListApps()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := []map[string]any{}
	for _, app := range apps {
		out = append(out, map[string]any{
			"id": app.ID, "app_name": app.Name, "status": app.Status,
			"current_version": app.CurrentVersion, "port": app.Port,
			"access_url": "http://" + r.Host + app.RootPath,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
