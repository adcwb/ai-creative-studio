import { describe, expect, it } from 'vitest'
import { fieldLengthError, generationActive, projectDraftError, scriptDraftError, type DramaProject, type DramaScript } from './drama'
const script: DramaScript = { title: '合成剧本', logline: '三场景的合成验收故事。', characters: [{name:'甲',description:'合成角色甲'}, {name:'乙',description:'合成角色乙'}], scenes: [1,2,3].map(n => ({heading:`第${n}场`,action:'合成场景动作',dialogue:[{character:'甲',text:'合成对白'}]})) }
describe('short drama editable content and generation state', () => {
  it('uses trimmed Unicode lengths for brief and title limits', () => {
    expect(projectDraftError({title:' 标题 ',brief:' 😀😀😀 '})).toBe('')
    expect(projectDraftError({title:' ',brief:'合法简报'})).not.toBe('')
    expect(projectDraftError({title:'标题',brief:'😀'.repeat(2000)})).toBe('')
    expect(projectDraftError({title:'标题',brief:'😀'.repeat(2001)})).not.toBe('')
    expect(fieldLengthError('😀'.repeat(80),1,80,'标题')).toBe('')
  })
  it('validates every editable scene and dialogue rather than only the first one', () => {
    expect(scriptDraftError(script)).toBe('')
    const changed = structuredClone(script); changed.scenes[2].dialogue[0].text = '😀'.repeat(301)
    expect(scriptDraftError(changed)).toContain('第 3 场对白')
    changed.scenes[2].dialogue[0].text = '有效对白'; changed.scenes[1].action = ' '
    expect(scriptDraftError(changed)).toContain('第 2 场动作')
  })
  it('locks only queued or running generation while allowing explicit recovery from failure', () => {
    for (const status of ['queued','running','succeeded','failed'] as const) {
      const project = {latest_generation:{status}} as DramaProject
      expect(generationActive(project)).toBe(status === 'queued' || status === 'running')
    }
    expect(generationActive(null)).toBe(false)
  })
})
