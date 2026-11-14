import {Button, Table, Tag} from 'antd'
import {useI18n} from '../i18n'
import {gatewayVendorNames, type AIGateway, type ModelManagementReport} from '../gatewayModels'

export function GatewayTable({report, busy, onEdit, onCheck, onSync, onAddModel}: {
  report: ModelManagementReport; busy: boolean; onEdit: (gateway: AIGateway) => void; onCheck: (gateway: AIGateway) => void; onSync: (gateway: AIGateway) => void; onAddModel: (gateway: AIGateway) => void
}) {
  const {t,formatDate} = useI18n()
  return <Table<AIGateway> rowKey="id" className="gateway-table studio-gateway-table" dataSource={report.gateways} pagination={false} columns={[
    {title:t('网关名称'),key:'name',render:(_,item)=><><strong>{item.name}</strong><small>{t(gatewayVendorNames[item.vendor ?? 'openai-compatible'])}</small>{item.last_synced_at && <small>{t('上次同步')} {formatDate(item.last_synced_at)}</small>}{item.sync_error && <small>{t('上次同步失败，请重试')}</small>}</>},
    {title:t('服务地址'),dataIndex:'base_url',key:'address',render:(value:string)=><span className="gateway-address">{value}</span>},
    {title:t('凭据'),key:'secret',render:(_,item)=><Tag color={item.secret_status === 'encrypted' ? 'success' : 'default'}>{t(item.secret_status === 'encrypted' ? '已加密' : item.secret_status === 'revoked' ? '已撤销' : '未配置')}</Tag>},
    {title:t('模型数'),key:'count',render:(_,item)=>report.models.filter(model=>model.gateway_id === item.id).length},
    {title:t('状态'),key:'state',render:(_,item)=><Tag color={item.enabled ? 'success' : 'default'}>{t(item.enabled ? '已启用' : '已停用')}</Tag>},
    {title:t('操作'),key:'actions',render:(_,item)=><div className="gateway-actions"><Button disabled={busy} onClick={()=>onEdit({...item})}>{t('编辑')}</Button><Button disabled={busy || !report.secret_storage_available || item.secret_status !== 'encrypted'} onClick={()=>onCheck(item)}>{t('检查连接')}</Button><Button disabled={busy || !report.secret_storage_available || !item.enabled || item.secret_status !== 'encrypted'} onClick={()=>onSync(item)}>{t('同步模型')}</Button><Button disabled={busy} onClick={()=>onAddModel(item)}>{t('添加模型')}</Button></div>},
  ]}/>
}
