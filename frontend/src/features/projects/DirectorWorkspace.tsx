import {useState} from 'react'
import {request,errorMessage,baseURL} from '../../api'
import {useI18n} from '../../i18n'
import type {StoryboardPlan,StoryboardShot} from '../../drama'
import {directorControlLabels,directorFieldLabels,directorModeLabels,directorValues,type DirectorMode,type DirectorSnapshot} from '../../director'
interface DirectorAsset{id:string;kind:string;name:string;file_url:string}
interface Selection{shot_id:string;asset_id:string}
export function DirectorDirection({shot}:{shot:StoryboardShot}){
 const {t}=useI18n()
 return <details><summary>{t('导演意图与能力边界')}</summary><p>{t('这些是创作意图。提示词不能保证精确机位、动作、时长或人物一致性；未支持的控制不会伪装成原生参数。')}</p>{directorValues(shot).map(([key,label])=><p key={key}><strong>{t(label)}</strong>：{shot[key as keyof typeof directorFieldLabels]}</p>)}</details>
}
export function DirectorSupport({controls}:{controls?:Record<string,DirectorMode>}){
 const {t}=useI18n()
 return <details className="reference-controls"><summary>{t('查看本次模型的导演控制')}</summary><p>{t('能力以当前选中的模型和实际适配器为准；没有声明支持的字段不作为原生控制发送。声音意图与对白不会夹带到画面提示词中。')}</p>{!controls?<p role="status">{t('请选择模型并等待能力读取；暂不能确认导演控制。')}</p>:<dl>{Object.entries(directorControlLabels).map(([key,label])=><div key={key}><dt>{t(label)}</dt><dd>{t(directorModeLabels[controls[key]]??directorModeLabels.unsupported)}</dd></div>)}</dl>}</details>
}
export function ShotNeighbours({board,shot,assets,selections,onCompare}:{board:StoryboardPlan;shot:string;assets:DirectorAsset[];selections:Selection[];onCompare:(id:string)=>void}){
 const {t}=useI18n(),index=board.shots.findIndex(v=>v.id===shot)
 if(index<0)return null
 const sides=[{label:'上一镜',item:board.shots[index-1]},{label:'下一镜',item:board.shots[index+1]}]
 return <details className="reference-controls"><summary>{t('前后镜头的衔接')}</summary><p>{t('对照相邻镜头的已采用画面，检查视线、轴线和动作衔接。比较不改采用记录或剪辑。')}</p><div className="candidate-view is-comparing">{sides.map(({label,item})=>{
  const selection=item?selections.filter(v=>v.shot_id===item.id).at(-1):undefined,asset=assets.find(v=>v.id===selection?.asset_id)
  return <section key={label}><h4>{t(label)}</h4>{item?<><p>{item.description}</p>{asset?<><figure>{asset.kind==='image'?<img loading="lazy" decoding="async" src={`${baseURL}${asset.file_url}`} alt={asset.name}/>:<video controls preload="metadata" src={`${baseURL}${asset.file_url}`}/>}<figcaption>{asset.name}</figcaption></figure><button type="button" onClick={()=>onCompare(asset.id)}>{t('与此镜头采用版对照')}</button></>:<p>{t('相邻镜头尚未采用画面。')}</p>}</>:<p>{t('已到镜头计划的边界。')}</p>}</section>
 })}</div></details>
}
export function DirectorSource({space,project,job,version}:{space:string;project:string;job:string;version:number}){
 const {t}=useI18n(),[value,setValue]=useState<DirectorSnapshot|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false)
 async function read(){setBusy(true);setError('');try{const v=await request<{director_context?:DirectorSnapshot}>(`/projects/${project}/creative-jobs/${job}?space_id=${encodeURIComponent(space)}`);if(!v.director_context)throw new Error(t('本次生成没有可读取的固定导演资料。'));setValue(v.director_context)}catch(e){setError(errorMessage(e,t('导演来源读取失败')))}finally{setBusy(false)}}
 return <details><summary>{t('固定导演来源 · 项目版本 {p0}',{p0:version})}</summary><p>{t('历史来源保持生成当时的镜头与前后关系，之后改稿不会替换这份记录。它不表示现在仍是最新内容。')}</p><button type="button" disabled={busy} onClick={()=>void read()}>{t(busy?'正在读取资料…':'读取这次固定的镜头资料')}</button>{error&&<p className="error" role="alert">{error}</p>}{value&&<><p>{value.current.description}</p><DirectorDirection shot={value.current}/><p>{t('未作为画面参数发送：{p0}',{p0:value.omitted_fields.map(k=>t(k==='dialogue'?'对白':k==='duration_seconds'?'镜头时长意图':directorFieldLabels[k as keyof typeof directorFieldLabels]??k)).join('、')})}</p>{value.omitted_neighbours?.length&&<p>{t('归属不同或无法确认的相邻镜头未用于本次生成。')}</p>}<p>{t('上一镜')}：{value.previous?.description??t('无')}</p><p>{t('下一镜')}：{value.next?.description??t('无')}</p></>}</details>
}
