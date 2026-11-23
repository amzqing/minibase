const http = require('http');
const port = Number(process.env.PORT || 3000);
let healthy = true;
setTimeout(() => { healthy = false; }, 3000); // 3秒后开始卡死
http.createServer((req, res) => {
  if (!healthy && req.url !== '/healthz') return; // 卡死：不响应
  res.end('ok');
}).listen(port, '127.0.0.1');
