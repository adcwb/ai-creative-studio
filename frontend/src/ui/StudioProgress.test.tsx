import {renderToStaticMarkup} from 'react-dom/server'
import {expect, test} from 'vitest'
import {StudioProgress} from './StudioProgress'

// The API writes 0/35/100 as stage markers. Rendering a percentage claimed a
// precision the backend does not provide, so these cases pin the stage wording.
test('each progress marker maps to a stage and never prints a percentage', () => {
  for (const [progress, stage] of [[0, '已接单'], [35, '生成中'], [100, '已完成']] as const) {
    const html = renderToStaticMarkup(<StudioProgress progress={progress} status={progress === 100 ? 'succeeded' : progress === 35 ? 'running' : 'queued'} label="进度" />)
    expect(html).toContain(stage)
    expect(html).not.toContain('%')
    expect(html).toContain('data-state="active"')
  }
})

test('a failure marks the reached stage and says the content is kept', () => {
  const html = renderToStaticMarkup(<StudioProgress progress={35} status="failed" label="进度" />)
  expect(html).toContain('data-failed="true"')
  expect(html).toContain('data-state="failed"')
  expect(html).toContain('本次未返回结果，内容已保留。')
  expect(html).not.toContain('%')
})
