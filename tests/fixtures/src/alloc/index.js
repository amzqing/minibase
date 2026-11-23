const http = require('http');
const port = Number(process.env.PORT || 3000);
const blocks = [];
http.createServer((req, res) => {
  if (req.url === '/alloc') {
    for (let i = 0; i < 100; i++) { const b = Buffer.alloc(10 * 1024 * 1024); b.fill(1); blocks.push(b); }
    res.end('allocated');
    return;
  }
  res.end('ok');
}).listen(port, '127.0.0.1');
