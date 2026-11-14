import {useI18n} from '../../i18n'
import type {Variable} from './types'
export function VariableDefinitions({items,disabled,change}:{items:Variable[];disabled:boolean;change:(items:Variable[])=>void}){
 const {t}=useI18n()
 const patch=(index:number,value:Partial<Variable>)=>change(items.map((v,i)=>i===index?{...v,...value}:v))
 return <details className="editing-note"><summary>{t('设置运行变量')}</summary><p>{t('将每次变化的主体、场景或语气设为变量。不要填入密钥；运行记录会保存输入。')}</p><fieldset disabled={disabled}>{items.map((v,i)=><div className="storyboard-fields" key={i}><label>{t('变量标识')}<input value={v.name} maxLength={32} pattern="[a-z][a-z0-9_]{0,31}" onChange={e=>patch(i,{name:e.target.value})}/></label><label>{t('变量显示名称')}<input value={v.label} maxLength={80} onChange={e=>patch(i,{label:e.target.value})}/></label><label>{t('默认文案')}<input value={v.default??''} maxLength={2000} onChange={e=>patch(i,{default:e.target.value})}/></label><label><input type="checkbox" checked={v.required} onChange={e=>patch(i,{required:e.target.checked})}/>{t('运行时必填')}</label><button type="button" onClick={()=>change(items.filter((_,n)=>n!==i))}>{t('移除变量')}</button></div>)}<button type="button" onClick={()=>change([...items,{name:'value_'+(items.length+1),label:'',required:true}])}>{t('添加运行变量')}</button></fieldset></details>
}
export function RunInputs({items,values,disabled,change}:{items:Variable[];values:Record<string,string>;disabled:boolean;change:(values:Record<string,string>)=>void}){
 const {t}=useI18n()
 if(!items.length)return null
 return <section className="editing-note" aria-label={t('本次运行输入')}><h3>{t('本次运行输入')}</h3><p>{t('每次运行固定这份输入；修改这里不会更改旧运行或已保存模板。')}</p><fieldset disabled={disabled}>{items.map(v=><label key={v.name}>{v.label||v.name}<input required={v.required} maxLength={2000} value={values[v.name]??v.default??''} onChange={e=>change({...values,[v.name]:e.target.value})}/></label>)}</fieldset></section>
}
