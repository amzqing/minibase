const http = require('http');
const port = Number(process.env.PORT || 3000);
http.createServer((req, res) => {
  let body = '';
  req.on('data', c => body += c);
  req.on('end', () => {
    res.writeHead(200, { 'Content-Type': 'application/json', 'X-Echo': 'yes' });
    res.end(JSON.stringify({ path: req.url, method: req.method, headers: req.headers, body }));
  });
}).listen(port, '127.0.0.1');
