import { Link } from 'react-router-dom'
import { useI18n } from '../i18n'

export function RouteErrorPage() {
  const {t} = useI18n()
  return <section className="panel page-empty" role="alert"><h1>{t('页面暂时无法加载')}</h1><p>{t('请重新加载页面，或返回创作空间继续。')}</p><button type="button" onClick={() => window.location.reload()}>{t('重新加载页面')}</button> <Link className="button-link" to="/studio">{t('返回图像创作')}</Link></section>
}
