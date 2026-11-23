const http = require('http');
const port = Number(process.env.PORT || 3000);
http.createServer((req, res) => {
  if (req.url === '/health') { res.end('ok'); return; }
  // 假死：永不响应
}).listen(port, '127.0.0.1');
