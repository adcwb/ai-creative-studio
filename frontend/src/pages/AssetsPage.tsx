import { useNavigate } from 'react-router-dom'
import { AssetLibrary } from '../components/AssetLibrary'
import { useImageWorkspace } from '../features/images/useImageWorkspace'
import { useI18n } from '../i18n'

export function AssetsPage() {
  const {t} = useI18n()
  const navigate = useNavigate()
  const {space, studio, openDetail} = useImageWorkspace()
  return <div className="assets-page"><header className="page-heading"><div><h1>{t('素材库')}</h1><p>{t('集中浏览当前工作空间已保存的图像素材。')}</p></div></header><AssetLibrary space={space} catalog={studio.catalog} total={studio.assets.length} onCreate={() => {void navigate('/studio')}} onDetail={openDetail} /></div>
}
