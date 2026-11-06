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
    expect(storyboardDraftError([{...shot,duration_seconds:121}])).toBe('')
    for (const scene_index of [-1, 1.5, Infinity]) expect(storyboardDraftError([{...shot, scene_index}])).toContain('引用')
    for (const duration_seconds of [0, Number.MAX_SAFE_INTEGER+1, 1.5, NaN]) expect(storyboardDraftError([valid[0], {...valid[1], duration_seconds}])).toContain('第 2 镜时长')
    expect(storyboardDraftError([valid[0], {...valid[1], description: ' '}])).toContain('第 2 镜画面描述')
  })
  it('rejects invalid identity, cardinality and unsupported choices', () => {
    expect(storyboardDraftError([])).not.toBe('')
    expect(storyboardDraftError(Array.from({length: 121}, (_, n) => ({...shot, id: `shot-${n}`})))).toBe('')
    expect(storyboardDraftError([shot, shot])).toContain('重复')
    expect(storyboardDraftError([{...shot, id: 'bad id'}])).toContain('标识')
    expect(storyboardDraftError([{...shot, shot_size: 'invalid' as StoryboardShot['shot_size']}])).toContain('景别')
    expect(storyboardDraftError([{...shot, camera_movement: 'toString' as StoryboardShot['camera_movement']}])).toContain('运镜')
  })
  it('validates complete director notes while keeping authoring separate from native controls',()=>{
    const directed={...shot,angle:'低机位',composition:'门框内保留留白',axis:'保持轴线',eyeline:'看向画右',blocking:'门前停顿',lighting:'暖光',sound:'只保存声音意图',performance:'迟疑'}
    expect(storyboardDraftError([directed])).toBe('')
    expect(storyboardDraftError([shot,{...directed,id:'second',lighting:'😀'.repeat(1001)}])).toContain('导演说明')
    expect(storyboardDraftError([{...directed,lighting:' 😀 '.repeat(1000)}])).toContain('导演说明')
    expect(storyboardDraftError([{...directed,lighting:' '+ '😀'.repeat(1000)+' '}])).toBe('')
  })
  it('locks project writes for either active generation and releases after failure', () => {
    for (const status of ['queued', 'running', 'succeeded', 'failed'] as const) {
      expect(generationActive({latest_storyboard_generation: {status}} as DramaProject)).toBe(status === 'queued' || status === 'running')
    }
    expect(generationActive({latest_generation: {status: 'running'}, latest_storyboard_generation: {status: 'failed'}} as DramaProject)).toBe(true)
    expect(generationActive({latest_generation: {status: 'failed'}, latest_storyboard_generation: {status: 'running'}} as DramaProject)).toBe(true)
  })
})
