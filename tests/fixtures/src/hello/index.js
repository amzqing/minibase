const http = require('http');
const port = Number(process.env.PORT || 3000);
http.createServer((req, res) => {
  if (req.url.startsWith('/api/users')) {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify([{ id: 1, name: 'Alice' }]));
    return;
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ path: req.url, mount: process.env.MOUNT_PATH || '' }));
}).listen(port, '127.0.0.1');
