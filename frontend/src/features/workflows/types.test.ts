import {expect,test} from 'vitest'
import {canConnect,template,type Graph} from './types'

test('typed media connections preserve human review and reject duplicates and cycles',()=>{
 const graph=template(true)
 expect(canConnect(graph,{source:'source',target:'output',input:'media'})).toBe(false)
 const free:Graph={...graph,edges:graph.edges.slice(0,1)}
 expect(canConnect(free,{source:'review',target:'output',input:'media'})).toBe(true)
 expect(canConnect(free,{source:'review',target:'source',input:'media'})).toBe(false)
 expect(canConnect(free,{source:'source',target:'source',input:'media'})).toBe(false)
})
test('a text label cannot stand in for image media and templates contain no generated output',()=>{
 const graph=template()
 expect(canConnect({...graph,edges:[]},{source:'description',target:'review',input:'media'})).toBe(false)
 expect(canConnect({...graph,edges:[]},{source:'description',target:'image',input:'prompt'})).toBe(true)
 expect(graph.nodes.some(n=>n.asset_id)).toBe(false)
 const chain:Graph={nodes:[{id:'a',kind:'review',label:'A',x:0,y:0},{id:'b',kind:'review',label:'B',x:100,y:0}],edges:[{source:'a',target:'b',input:'media'}]}
 expect(canConnect(chain,{source:'b',target:'a',input:'media'})).toBe(false)
})
