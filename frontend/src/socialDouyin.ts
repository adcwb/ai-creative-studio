// A capability link is used only after an explicit click, never on load. It is
// retained in memory, not localStorage/history or third-party analytics.
export function safeDouyinHandoff(value: string): boolean {
  try {
    const url = new URL(value)
    if (url.protocol !== 'snssdk1128:' || url.hostname !== 'openplatform' || url.pathname !== '/share' || url.username || url.password || url.hash || url.port) return false
    const sources = ['image_path', 'video_path'].flatMap(key => url.searchParams.getAll(key))
    if (sources.length !== 1) return false
    const source = new URL(sources[0])
    return source.protocol === 'https:' && !source.username && !source.password && !source.hash && source.pathname === '/api/v1/social-media/douyin' && source.searchParams.size === 1 && source.searchParams.has('cap') && (source.searchParams.get('cap')?.length ?? 0) <= 4096
  } catch {return false}
}
