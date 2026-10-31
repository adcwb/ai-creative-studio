import http from 'node:http'
let calls=0
http.createServer((req,res)=>{
 res.setHeader('Content-Type','application/json')
 if(req.url==='/__health'){res.end('{}');return}
 if(req.url==='/__state'){res.end(JSON.stringify({calls}));return}
 calls++
 if(req.url==='/v1/models' && ['Bearer synthetic-key-A','Bearer synthetic-key-B'].includes(req.headers.authorization)) {res.end(JSON.stringify({data:[{id:'synthetic-model'}]}));return}
 res.writeHead(401);res.end(JSON.stringify({error:'Synthetic authentication rejected'}))
}).listen(5193,'127.0.0.1')
