// Two separate same-site origins; this fixture stores booleans, never cookies/tokens.
import http from 'node:http'
let observed={saw_cookie:false,status:0}
http.createServer((_req,res)=>{res.writeHead(200,{'Content-Type':'text/html'});res.end('<!doctype html><html><title>Local untrusted origin fixture</title><h1>Untrusted test origin</h1></html>')}).listen(5185,'127.0.0.1')
http.createServer((req,res)=>{
 if(req.url==='/__observations'){res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify(observed));return}
 observed={saw_cookie:Boolean(req.headers.cookie?.includes('studio_session=')),status:0}
 const forwarded=http.request({hostname:'127.0.0.1',port:8082,path:req.url,method:req.method,headers:{...req.headers,host:'localhost:8082'}},upstream=>{observed.status=upstream.statusCode??0;res.writeHead(upstream.statusCode??502,upstream.headers);upstream.pipe(res)})
 forwarded.on('error',()=>{res.writeHead(502);res.end()});req.pipe(forwarded)
}).listen(5186,'127.0.0.1')
