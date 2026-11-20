import {renderToStaticMarkup} from 'react-dom/server'
import {expect, test} from 'vitest'
import {ParamSnapshot, type SnapshotJob} from './ParamSnapshot'

const job: SnapshotJob = {
  id: 'job-1', asset_id: 'asset-1', kind: 'video', shot_id: 'shot-3',
  prompt: '清晨薄雾中的钟楼', prompt_source: 'template', director_context: '缓慢推近',
  reference_asset_ids: ['ref-1', 'ref-2'], first_frame_asset_id: 'frame-a',
  library_versions: [{asset_id: 'lib-1', version_id: 'ver-9'}],
}

test('the snapshot surfaces what a revision was actually generated from', () => {
  const html = renderToStaticMarkup(<ParamSnapshot jobs={[job]} />)
  expect(html).toContain('shot-3')
  expect(html).toContain('清晨薄雾中的钟楼')
  expect(html).toContain('缓慢推近')
  expect(html).toContain('frame-a')
  expect(html).toContain('ver-9')
  expect(html).toContain('<details')
})

test('no jobs means no empty disclosure', () => {
  expect(renderToStaticMarkup(<ParamSnapshot jobs={[]} />)).toBe('')
})
