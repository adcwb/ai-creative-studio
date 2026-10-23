import { describe, expect, it } from 'vitest'
import { generationActive, storyboardDraftError, type DramaProject, type StoryboardShot } from './drama'

const shot: StoryboardShot = {id: 'shot-1', scene_index: 1, description: '合成镜头画面', shot_size: 'wide', camera_movement: 'static', duration_seconds: 6, dialogue: ''}

describe('storyboard bounded editing', () => {
  it('accepts optional dialogue and counts trimmed Unicode code points', () => {
    expect(storyboardDraftError([shot])).toBe('')
    expect(storyboardDraftError([{...shot, description: ` ${'😀'.repeat(1000)} `, dialogue: '😀'.repeat(2000)}])).toBe('')
    expect(storyboardDraftError([{...shot, description: '😀'.repeat(1001)}])).toContain('画面描述')
    expect(storyboardDraftError([{...shot, dialogue: '😀'.repeat(2001)}])).toContain('对白')
  })
  it('validates every shot, bounded references and integer durations', () => {
    const valid = [{...shot}, {...shot, id: 'shot-2', scene_index: 3, duration_seconds: 60}]
    expect(storyboardDraftError(valid)).toBe('')
    for (const scene_index of [0, 4, 1.5]) expect(storyboardDraftError([{...shot, scene_index}])).toContain('引用')
    for (const duration_seconds of [0, 61, 1.5, NaN]) expect(storyboardDraftError([valid[0], {...valid[1], duration_seconds}])).toContain('第 2 镜时长')
    expect(storyboardDraftError([valid[0], {...valid[1], description: ' '}])).toContain('第 2 镜画面描述')
  })
  it('rejects invalid identity, cardinality and unsupported choices', () => {
    expect(storyboardDraftError([])).not.toBe('')
    expect(storyboardDraftError(Array.from({length: 31}, (_, n) => ({...shot, id: `shot-${n}`})))).not.toBe('')
    expect(storyboardDraftError([shot, shot])).toContain('重复')
    expect(storyboardDraftError([{...shot, id: 'bad id'}])).toContain('标识')
    expect(storyboardDraftError([{...shot, shot_size: 'invalid' as StoryboardShot['shot_size']}])).toContain('景别')
    expect(storyboardDraftError([{...shot, camera_movement: 'toString' as StoryboardShot['camera_movement']}])).toContain('运镜')
  })
  it('locks project writes for either active generation and releases after failure', () => {
    for (const status of ['queued', 'running', 'succeeded', 'failed'] as const) {
      expect(generationActive({latest_storyboard_generation: {status}} as DramaProject)).toBe(status === 'queued' || status === 'running')
    }
    expect(generationActive({latest_generation: {status: 'running'}, latest_storyboard_generation: {status: 'failed'}} as DramaProject)).toBe(true)
    expect(generationActive({latest_generation: {status: 'failed'}, latest_storyboard_generation: {status: 'running'}} as DramaProject)).toBe(true)
  })
})
