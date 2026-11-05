import {useState} from 'react'
import {useI18n} from '../../i18n'
import {useAppWorkspace} from '../../app/workspace'
import {defaultProfile,episodeStatus,type OutputProfile,type SeriesEntry} from './types'
import {useSeriesWrite} from './useSeriesWrite'

export function ProfileFields({value,onChange}:{value:OutputProfile;onChange:(v:OutputProfile)=>void}){
 const {t}=useI18n()
 const base=({ '1080p':[1920,1080],'1440p':[2560,1440],'1800p':[3200,1800],'2160p':[3840,2160] })[value.output_resolution]??[0,0]
 const width=value.aspect_ratio==='16:9'?base[0]:base[1],height=value.aspect_ratio==='9:16'?base[0]:base[1]
 return <><div className="series-profile"><label>{t('目标时长（秒）')}<input type="number" min="1" step="1" value={value.duration_seconds||''} onChange={e=>onChange({...value,duration_seconds:Number(e.target.value)})}/></label><label>{t('默认成片清晰度')}<select value={value.output_resolution} onChange={e=>onChange({...value,output_resolution:e.target.value as OutputProfile['output_resolution']})}>{[['1080p','1K · 1080p'],['1440p','2K · 1440p'],['1800p','3K · 1800p'],['2160p','4K · 2160p']].map(([v,label])=><option key={v} value={v}>{label}</option>)}</select></label><label>{t('默认画幅')}<select value={value.aspect_ratio} onChange={e=>onChange({...value,aspect_ratio:e.target.value as OutputProfile['aspect_ratio']})}><option value="9:16">{t('竖屏 9:16')}</option><option value="16:9">{t('横屏 16:9')}</option><option value="1:1">{t('方形 1:1')}</option></select></label></div><p className="drama-help">{t('默认输出：{width} × {height} 像素',{width,height})}</p></>
}
export function SeriesForm({kind,item,parent,profile=defaultProfile,onSaved,onCancel,onRefresh}:{kind:SeriesEntry['kind'];item?:SeriesEntry;parent?:string;profile?:OutputProfile;onSaved:(n:SeriesEntry)=>void;onCancel:()=>void;onRefresh?:()=>void}){
 const {t}=useI18n();const {space,requestLeave}=useAppWorkspace()
 const path=item?`/${kind==='series'?'series':kind==='season'?'seasons':'episodes'}/${item.id}`:kind==='series'?'/series':kind==='season'?`/series/${parent}/seasons`:`/seasons/${parent}/episodes`
 const write=useSeriesWrite(`${item?.id??`new:${kind}:${parent??''}`}`,path,item?'PATCH':'POST',onSaved)
 const seed=write.recovery
 const [title,setTitle]=useState(()=>String(seed?.title??item?.title??'')),[summary,setSummary]=useState(()=>String(seed?.summary??item?.summary??'')),[audience,setAudience]=useState(()=>String(seed?.audience??item?.audience??'')),[format,setFormat]=useState(()=>String(seed?.format??item?.format??'motion-comic'))
 const [output,setOutput]=useState<OutputProfile>(()=>(seed?.profile as OutputProfile|undefined)??item?.profile??profile)
 const [status,setStatus]=useState(()=>String(seed?.status??item?.status??'active'))
 const {busy,uncertain,error}=write
 const minTitle=kind==='season'?0:1, minSummary=kind==='series'||(kind==='episode'&&summary.trim()!=='')?3:0, maxSummary=kind==='episode'?2000:300
 const metadataOnly=kind==='episode'&&Boolean(item)
 const invalid=(!metadataOnly&&([...title.trim()].length<minTitle||[...title.trim()].length>80||[...summary.trim()].length<minSummary||[...summary.trim()].length>maxSummary))||[...audience.trim()].length>400||!['1080p','1440p','1800p','2160p'].includes(output.output_resolution)||!['16:9','9:16','1:1'].includes(output.aspect_ratio)||!Number.isSafeInteger(output.duration_seconds)||output.duration_seconds<1
 const labels={series:item?'编辑系列':'新建系列',season:item?'编辑这一季':'添加一季',episode:item?'剧集制作设置':'添加一集'}
 const change=(f:()=>void)=>{write.markDirty();f()}
 return <section className="panel series-form"><h2>{t(labels[kind])}</h2><p className="drama-help">{metadataOnly?t('标题和故事简述请在这一集的项目总览中编辑。'):t('保存目录不会自动调用模型。创作内容由你决定。')}</p>
 <form onSubmit={e=>{e.preventDefault();if(!invalid||uncertain)void write.save({...(!metadataOnly?{title,summary}:{}),...(kind==='series'?{audience,format}:{}),profile:output,...(item?{expected_version:item.row_version,status}:{})})}}>
 <fieldset disabled={busy||uncertain||space?.role==='viewer'}>
 {!metadataOnly&&<><label>{t('名称')}<input value={title} maxLength={160} onChange={e=>change(()=>setTitle(e.target.value))} required={kind!=='season'} autoFocus/></label><label>{kind==='episode'?t('本集故事简述'):t('故事简介')}<textarea value={summary} onChange={e=>change(()=>setSummary(e.target.value))} required={kind==='series'}/></label>{kind==='episode'&&<p className="drama-help">{t('留空时沿用本季简介；保存后可在项目总览细化。')}</p>}</>}
 {kind==='series'&&<><label>{t('目标观众（选填，最多400字）')}<input value={audience} onChange={e=>change(()=>setAudience(e.target.value))}/></label><label>{t('表现形式')}<select value={format} onChange={e=>change(()=>setFormat(e.target.value))}><option value="motion-comic">{t('AI 漫剧')}</option><option value="video">{t('视频短剧')}</option><option value="mixed">{t('混合制作')}</option></select></label></>}
 <ProfileFields value={output} onChange={v=>change(()=>setOutput(v))}/><p className="drama-help">{t('建议每集1–2分钟。清晰度是后期输出档位，不代表模型原生画质。已有时间线不会随默认设置改变。')}</p>
 {item&&<label>{t('创作状态')}<select value={status} onChange={e=>change(()=>setStatus(e.target.value))}>{(kind==='episode'?Object.entries(episodeStatus):[['active','创作中'],['archived','已归档']]).map(([v,label])=><option key={v} value={v}>{t(label)}</option>)}</select></label>}
 </fieldset>
 {invalid&&write.dirty&&!uncertain&&<p className="drama-validation" role="status">{t('名称最多80字；系列简介3–300字，剧集简述可留空或填写3–2000字，时长需为正整数。')}</p>}
 {error&&<div className="error" role="alert">{error}{!uncertain&&onRefresh&&<button type="button" onClick={onRefresh}>{t('重新读取目录版本')}</button>}</div>}
 {write.storageNotice&&<p className="editing-note" role="status">{write.storageNotice}</p>}
 {uncertain&&<p className="editing-note" role="status">{t('上次保存结果尚未确认。请用原请求核对，避免重复创建。')}</p>}
 <div className="drama-brief-actions"><button type="button" disabled={busy} onClick={()=>requestLeave(onCancel)}>{t('取消')}</button><button className="primary" disabled={busy||(!uncertain&&invalid)||space?.role==='viewer'}>{busy?t('正在保存…'):uncertain?t('核对并重试保存'):t('保存')}</button></div>
 </form></section>
}
export function SeriesMoveForm({item,parent,onSaved,onCancel}:{item:SeriesEntry;parent:SeriesEntry;onSaved:(n:SeriesEntry)=>void;onCancel:()=>void}){
 const {t}=useI18n();const {requestLeave}=useAppWorkspace()
 const path=item.kind==='season'?`/series/${parent.id}/season-order`:`/seasons/${parent.id}/episode-order`
 const write=useSeriesWrite(`move:${item.id}`,path,'POST',onSaved)
 const [ordinal,setOrdinal]=useState(()=>Number(write.recovery?.ordinal??item.ordinal))
 const [mode,setMode]=useState(()=>String(write.recovery?.mode??'swap'))
 return <section className="panel series-form"><h2>{t(item.kind==='season'?'调整季号':'调整集号')}</h2><form onSubmit={e=>{e.preventDefault();void write.save({id:item.id,ordinal,expected_version:item.row_version,expected_parent_version:parent.row_version,mode})}}><fieldset disabled={write.busy||write.uncertain}><label>{t('新的编号')}<input type="number" min="1" step="1" value={ordinal} onChange={e=>{setOrdinal(Number(e.target.value));write.markDirty()}}/></label><label>{t('调整方式')}<select value={mode} onChange={e=>{setMode(e.target.value);write.markDirty()}}><option value="swap">{t('交换编号')}</option><option value="insert">{t('插入并顺延')}</option></select></label><p className="drama-help">{t(mode==='insert'?'插到目标位置，区间内其他内容顺移。作品身份和旧成片编号保持不变。':'目标编号已有内容时，两者交换编号。旧成片保留导出时编号。')}</p></fieldset>{write.error&&<p className="error" role="alert">{write.error}</p>}<div className="drama-brief-actions"><button type="button" disabled={write.busy} onClick={()=>requestLeave(onCancel)}>{t('取消')}</button><button className="primary" disabled={write.busy||!Number.isSafeInteger(ordinal)||ordinal<1}>{write.uncertain?t('核对并重试保存'):t('保存编号')}</button></div></form></section>
}
