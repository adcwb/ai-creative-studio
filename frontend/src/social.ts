export type SocialPlatform = 'tiktok' | 'douyin' | 'xiaohongshu'
export interface SocialIntent {privacy_level?: string; allow_comment?: boolean; allow_duet?: boolean; allow_stitch?: boolean; is_aigc?: boolean; brand_content?: boolean; brand_organic?: boolean}
export interface SocialJob {
  next_poll_at?: string; lease_until?: string
  intent?: SocialIntent
  id: string; platform: SocialPlatform; mode: string; status: string
  asset_id?: string; export_id?: string; title: string; caption: string
  download_url?: string; mime_type?: string; created_at: string; connection_id?: string; privacy_level?: string; allow_comment?: boolean; allow_duet?: boolean; allow_stitch?: boolean; is_aigc?: boolean; brand_content?: boolean; brand_organic?: boolean; publish_id?: string
}
export interface SocialState {
  version: number
  platforms: {id: SocialPlatform; direct_status: string; handoff_status: string; connection_status: string; reason_code?: string}[]
  jobs: SocialJob[]
  connections: SocialConnection[]
}
export interface SocialConnection {id: string; platform: SocialPlatform; status: string; display_name?: string; username?: string}
export interface SocialSource {id: string; kind: 'asset' | 'export'; name: string; mime_type: string; file_url?: string}
export interface SocialMedia {
  assets: {id: string; kind: string; name: string; mime_type: string; file_url?: string}[]
  exports: {id: string; status: string; file_url?: string}[]
}
export function socialSources(media: SocialMedia): SocialSource[] {
  return [...media.assets.filter(asset => asset.kind === 'image' || asset.kind === 'video').map(asset => ({id: asset.id, kind: 'asset' as const, name: asset.name, mime_type: asset.mime_type, file_url: asset.file_url})),
    ...media.exports.filter(item => item.status === 'succeeded').map(item => ({id: item.id, kind: 'export' as const, name: item.id, mime_type: 'video/mp4', file_url: item.file_url}))]
}
// Only authenticated application paths may become download links. Never trust an
// adapter response to navigate the user to a vendor or credential-bearing URL.
export function socialDownloadPath(value: string | undefined): string | null {
  if (!value || !value.startsWith('/api/v1/projects/') || value.includes('\\') || /[\r\n]/.test(value)) return null
  try {const parsed = new URL(value, 'https://studio.invalid'); return parsed.origin === 'https://studio.invalid' && parsed.pathname.startsWith('/api/v1/projects/') ? value : null} catch {return null}
}
export const socialPlatformLabels: Record<SocialPlatform, string> = {tiktok: 'TikTok', douyin: '抖音', xiaohongshu: '小红书'}
export const socialStatusLabels: Record<string, string> = {
  prepared: '素材已备好，尚未发布', handed_off: '已交接至平台，尚未确认发布', draft: '平台草稿，尚未发布',
  published: '平台已确认发布', failed: '发布失败', unknown: '结果未知，禁止盲目重发',
  approval_required: '需要平台审批', unavailable: '当前不可用', access_paused: '平台暂停接入', not_configured: '尚未配置账号连接',
  dispatching: '请求发送中', processing: '平台处理中', cancelled: '已取消', connected: '已连接', revoked: '已撤销', configured: '应用已配置', available: '能力已配置', export: '导出素材', direct: '直接发布', handoff: '转到平台 App',
}

export function safeTikTokAuthorization(value: string): boolean {
  try {const url = new URL(value); return url.protocol === 'https:' && url.hostname === 'www.tiktok.com' && !url.port && !url.username && !url.password && url.pathname === '/v2/auth/authorize/'} catch {return false}
}
