import { Link } from 'react-router-dom'
import { useI18n } from '../i18n'

export function NotFoundPage() {
  const {t} = useI18n()
  return <section className="page-empty"><h1>{t('找不到页面')}</h1><p>{t('链接可能已失效，请从左侧导航继续。')}</p><Link className="button-link primary" to="/studio">{t('返回图像创作')}</Link></section>
}
