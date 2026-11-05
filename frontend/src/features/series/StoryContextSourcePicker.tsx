import {useState} from 'react'
import {useI18n} from '../../i18n'
import {useDirectory} from './useDirectory'
import type {StorySummary} from './storyTypes'

export function StoryContextSourcePicker(props:{root:string;space:string;selected:string[];onChange:(ids:string[])=>void;known:StorySummary[];disabled:boolean}){
 const{t}=useI18n();const[open,setOpen]=useState(false)
 return <details onToggle={e=>{if(e.currentTarget.open)setOpen(true)}}><summary>{t('明确带入其他资料')}</summary><p className="drama-help">{t('正式资料目录可逐页加载。故事圣经与必需资料始终核对；明确选择也不会绕过叙事范围、归档或模型输入能力。')}</p>{open&&<Sources {...props}/>}</details>
}
function Sources({root,space,selected,onChange,known,disabled}:{root:string;space:string;selected:string[];onChange:(ids:string[])=>void;known:StorySummary[];disabled:boolean}){
 const{t}=useI18n();const[revision,setRevision]=useState(0)
 const list=useDirectory<StorySummary>(`/series/${root}/story-records`,space,revision)
 const labels=new Map([...known,...list.items].map(v=>[v.id,v.title]))
 return <section aria-label={t('手动选择正式资料')}><button type="button" disabled={list.loading||disabled} onClick={()=>setRevision(v=>v+1)}>{t('刷新可选资料，保留选择')}</button>
 {!!selected.length&&<section><h4>{t('本次明确选择')}</h4>{selected.map(id=><p key={id}>{labels.get(id)??t('尚未载入或已不可用的资料，请核对')} <button type="button" disabled={disabled} onClick={()=>onChange(selected.filter(v=>v!==id))}>{t('移除此项明确选择')}</button></p>)}</section>}
 {list.loading&&<p role="status">{t('正在读取故事资料…')}</p>}{list.error&&<p className="error" role="alert">{list.error}</p>}{!list.loading&&!list.error&&!list.items.length&&<p>{t('还没有正式资料。先保存故事圣经或一条设定。')}</p>}
 {list.items.map(v=><div key={v.id}><label className="story-check"><input type="checkbox" disabled={disabled||v.required||v.kind==='bible'||v.status!=='active'} checked={(v.status==='active'&&(v.required||v.kind==='bible'))||selected.includes(v.id)} onChange={e=>onChange(e.target.checked?[...selected.filter(id=>id!==v.id),v.id]:selected.filter(id=>id!==v.id))}/>{v.title}</label><p className="drama-help">{t('版本 {p0}',{p0:v.number})}{v.status==='active'&&(v.required||v.kind==='bible')?` · ${t('始终核对')}`:''}{v.status!=='active'?` · ${t('已归档')}`:''}</p></div>)}
 {list.next_cursor&&<button type="button" disabled={list.more||list.loading||disabled} onClick={()=>void list.loadMore()}>{t('加载更多资料')}</button>}
 </section>
}
