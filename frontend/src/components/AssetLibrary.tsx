import { useI18n } from '../i18n'
import { useEffect, useState } from 'react'
import { baseURL, errorMessage, kindLabels, request, type Asset, type Catalog } from '../api'
import { resultImageAlt, resultOriginLabel } from '../providerPresentation'
import { Icon } from './Icon'
import { TaskMetadata } from './TaskList'

export function AssetLibrary({space, catalog, total, onCreate, onDetail}: {space: string; catalog: Catalog | null; total: number; onCreate: () => void; onDetail: (id: string) => void}) {
  const { t } = useI18n()
  const [search, setSearch] = useState('')
  const [kind, setKind] = useState('')
  const [revision, setRevision] = useState(0)
  const [result, setResult] = useState<{key: string; assets: Asset[]} | null>(null)
  const [failure, setFailure] = useState<{key: string; message: string} | null>(null)
  const query = new URLSearchParams({space_id: space})
  if (search.trim()) query.set('q', search.trim())
  if (kind) query.set('kind', kind)
  const queryString = query.toString()
  const resultKey = `${queryString}:${revision}:${total}`
  const invalid = [...search.trim()].length > 100
  const hasFilter = !!search.trim() || !!kind
  const loaded = result?.key === resultKey
  const error = failure?.key === resultKey ? failure.message : ''

  useEffect(() => {
    if (invalid) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      void request<Asset[]>(`/assets?${queryString}`, {signal: controller.signal})
        .then(assets => {if (!controller.signal.aborted) {setResult({key: resultKey, assets}); setFailure(null)}})
        .catch(reason => {if (!controller.signal.aborted) setFailure({key: resultKey, message: errorMessage(reason, "素材查询失败")})})
    }, 250)
    return () => {clearTimeout(timer); controller.abort()}
  }, [queryString, resultKey, invalid])

  const kinds = catalog ? [...new Set(catalog.templates.map(template => template.kind))] : []
  return <section className="panel library" aria-labelledby="library-title">
    <div className="section-heading"><div><span className="eyebrow">COLLECT YOUR IDEAS</span><h2 id="library-title">{t("我的素材")}</h2></div><span>{t("当前空间 · 已保存 {count} 件",{count:total})}</span></div>
    <form className="library-filters" onSubmit={event => {event.preventDefault(); setRevision(value => value + 1)}} role="search" aria-label={t("查找已保存素材")}>
      <div className="search-field"><label htmlFor="asset-search">{t("搜索素材")}</label><div className="input-with-icon"><Icon name="search" /><input type="search" id="asset-search" value={search} onChange={event => setSearch(event.target.value)} placeholder={t("按画面描述搜索")} maxLength={200} aria-describedby={invalid ? 'search-error' : undefined} aria-invalid={invalid} /></div></div>
      <div><label htmlFor="asset-kind">{t("素材类型")}</label><select id="asset-kind" value={kind} onChange={event => setKind(event.target.value)}><option value="">{t("全部类型")}</option>{kinds.map(value => <option key={value} value={value}>{t(kindLabels[value])}</option>)}</select></div>
      <button type="button" className="clear-filters" disabled={!hasFilter && !search} onClick={() => {setSearch(''); setKind('')}}>{t("清除筛选")}</button>
    </form>
    {invalid ? <p id="search-error" className="error" role="alert">{t("搜索关键词最多 100 个字符。")}</p> : error ? <div className="error" role="alert">{t("无法查询素材：")}{t(error)} <button onClick={() => setRevision(value => value + 1)}>{t("重新查询")}</button></div> : !loaded ? <div className="loading-block" role="status">{t("正在查询素材…")}</div> : result.assets.length === 0 ? <div className="empty"><div className="empty-art" aria-hidden="true"><Icon name={hasFilter ? 'search' : 'grid'} /></div><h3>{hasFilter ? t("没有找到匹配素材") : t("为灵感留个位置")}</h3><p>{hasFilter ? t("试试其他关键词或类型，已保存的素材仍在库中。") : t("完成任务后保存到素材库，让每一次灵感都有迹可循。")}</p><button onClick={hasFilter ? () => {setSearch(''); setKind('')} : onCreate}>{hasFilter ? t("清除条件") : t("去创作第一张图像")}<Icon name="arrow" /></button></div> : <>
      <p className="query-summary" role="status">{t("找到 {count} 件素材",{count:result.assets.length})}{hasFilter ? t(" · 当前筛选结果") : ''}</p>
      <div className="asset-grid">{result.assets.map(asset => <article key={asset.id} className="asset-card" data-provider={asset.provider}><button type="button" className="asset-preview" aria-label={t("查看素材任务详情")} onClick={() => onDetail(asset.task_id)}><img src={`${baseURL}${asset.result_url}`} alt={resultImageAlt(asset)} /><span>{t("查看详情")}<Icon name="arrow" /></span></button><div className="asset-information"><span className="origin-label">{resultOriginLabel(asset)}</span><p>{asset.prompt}</p><TaskMetadata task={asset} catalog={catalog} /></div></article>)}</div>
    </>}
  </section>
}
