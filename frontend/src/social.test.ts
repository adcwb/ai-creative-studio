import { describe, expect, it } from 'vitest'
import { safeTikTokAuthorization, socialDownloadPath, socialSources, socialStatusLabels } from './social'
import social from './locales/social'

describe('social delivery safety', () => {
  it('only offers images, videos and succeeded exports', () => {
    expect(socialSources({assets: [{id: 'i', kind: 'image', name: 'Picture', mime_type: 'image/png'}, {id: 'v', kind: 'video', name: 'Video', mime_type: 'video/mp4'}, {id: 'a', kind: 'audio', name: 'Audio', mime_type: 'audio/wav'}], exports: [{id: 'done', status: 'succeeded'}, {id: 'wait', status: 'running'}, {id: 'failed', status: 'failed'}]}).map(item => item.id)).toEqual(['i', 'v', 'done'])
  })
  it('rejects remote, scheme-relative, normalized traversal and malformed download paths', () => {
    for (const path of ['https://evil.invalid/file', '//evil.invalid/file', 'javascript:alert(1)', '/api/v1/projects/../../../evil', '/api/v1/projects/\\evil', '/api/v1/projects/x\nfile', '/api/v1/models']) expect(socialDownloadPath(path)).toBeNull()
    expect(socialDownloadPath('/api/v1/projects/p/media/assets/a/file?space_id=tenant')).toBe('/api/v1/projects/p/media/assets/a/file?space_id=tenant')
  })
  it('only navigates to the exact TikTok OAuth origin and path', () => {
    expect(safeTikTokAuthorization('https://www.tiktok.com/v2/auth/authorize/?state=synthetic')).toBe(true)
    for (const url of ['https://tiktok.com/v2/auth/authorize/', 'https://www.tiktok.com.evil.invalid/v2/auth/authorize/', 'https://user:secret@www.tiktok.com/v2/auth/authorize/', 'http://www.tiktok.com/v2/auth/authorize/', 'https://www.tiktok.com:8080/v2/auth/authorize/']) expect(safeTikTokAuthorization(url)).toBe(false)
  })
  it('never labels export, draft or handoff as published', () => {
    expect(social[socialStatusLabels.prepared]).toBe('Media prepared, not published')
    expect(social[socialStatusLabels.handed_off]).toBe('Handed off, publication unconfirmed')
    expect(social[socialStatusLabels.draft]).toBe('Platform draft, not published')
    expect(social[socialStatusLabels.unknown]).toContain('do not blindly retry')
  })
})
