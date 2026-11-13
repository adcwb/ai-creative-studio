export type Kind='text'|'template'|'asset'|'image'|'speech'|'video'|'review'|'output'
export interface Step {id:string;kind:Kind;label:string;x:number;y:number;text?:string;asset_id?:string;asset_kind?:string;model_config_id?:string;model_config_version?:number;voice?:string;ratio?:string}
export interface Wire {source:string;target:string;input:string}
export interface Graph {nodes:Step[];edges:Wire[];viewport?:{x:number;y:number;zoom:number}}
export interface Summary {id:string;name:string;version:number;head_revision_id:string}
export interface Revision {id:string;number:number;sha256:string;graph:Graph;created_at:string;created_by:string}
export interface Detail extends Summary {revision:Revision;revision_count:number}
export interface Run {id:string;workflow_id:string;revision_id:string;status:string;version:number;created_at:string;graph:Graph;nodes:Record<string,{node_id:string;status:string;job_id?:string;error_code?:string;result:{text?:string;asset_id?:string;asset_kind?:string}}>}
export interface Asset {id:string;kind:string;name:string;file_url:string}
export const labels:Record<Kind,string>={text:'描述与文案',template:'文案模板',asset:'项目素材',image:'图像生成',speech:'配音生成',video:'视频生成',review:'人工选择',output:'输出素材'}
export const states:Record<string,string>={queued:'等待执行',running:'正在执行',waiting_review:'等待人工选择',succeeded:'已完成',failed:'执行失败',cancelled:'已停止',unknown:'结果待核对'}
export function ports(kind:Kind){return kind==='image'||kind==='video'?['prompt','reference']:kind==='speech'||kind==='template'?['prompt']:kind==='review'||kind==='output'?['media']:[]}
export const portLabels:Record<string,string>={prompt:'描述输入',reference:'参考图输入',media:'素材输入'}
export function template(media=false,t:(v:string)=>string=v=>v):Graph{const g:Graph=media?{nodes:[{id:'source',kind:'asset',label:'项目素材',asset_kind:'image',x:20,y:80},{id:'review',kind:'review',label:'人工选择',x:300,y:80},{id:'output',kind:'output',label:'输出素材',x:580,y:80}],edges:[{source:'source',target:'review',input:'media'},{source:'review',target:'output',input:'media'}]}:{nodes:[{id:'description',kind:'text',label:'描述与文案',text:'清晨的花园，柔和光线，画面安静自然。',x:20,y:80},{id:'image',kind:'image',label:'图像生成',ratio:'9:16',x:300,y:80},{id:'review',kind:'review',label:'人工选择',x:580,y:80},{id:'output',kind:'output',label:'输出素材',x:860,y:80}],edges:[{source:'description',target:'image',input:'prompt'},{source:'image',target:'review',input:'media'},{source:'review',target:'output',input:'media'}]};return {...g,nodes:g.nodes.map(n=>({...n,label:t(n.label),...(n.text?{text:t(n.text)}:{})}))}}
export function canConnect(graph:Graph,edge:Wire){
 const target=graph.nodes.find(n=>n.id===edge.target),source=graph.nodes.find(n=>n.id===edge.source)
 if(!target||!source||target.id===source.id||!ports(target.kind).includes(edge.input)||graph.edges.some(e=>e.target===edge.target&&e.input===edge.input))return false
 const seen=new Set<string>();function reaches(id:string):boolean{if(id===edge.source)return true;if(seen.has(id))return false;seen.add(id);return graph.edges.filter(e=>e.source===id).some(e=>reaches(e.target))}
 if(reaches(edge.target))return false
 function output(id:string,visited=new Set<string>()):string{if(visited.has(id))return '';visited.add(id);const n=graph.nodes.find(v=>v.id===id);if(!n)return '';if(n.kind==='text'||n.kind==='template')return 'text';if(n.kind==='asset')return n.asset_kind??'';if(n.kind==='speech')return 'audio';if(n.kind==='image'||n.kind==='video')return n.kind;const parent=graph.edges.find(e=>e.target===id&&e.input==='media');return parent?output(parent.source,visited):''}
 const type=output(edge.source)
 return edge.input==='prompt'?type==='text':edge.input==='reference'?type==='image':['image','audio','video'].includes(type)
}
