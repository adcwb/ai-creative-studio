import {test, expect} from '@playwright/test'

const fixture = 'http://127.0.0.1:5189'
const images = ['A','B','C'].map((name,i)=>({id:`image-${i}`,name:`参考${name}`,kind:'image',role:'shot',shot_id:'',character:'',source:'uploaded',mime_type:'image/png',file_url:'/fixture.svg',duration:0}))
test.beforeEach(async ({request,page})=>{
  await request.post(fixture+'/__test__/reset',{data:{}})
  page.on('pageerror',error=>{throw error})
})

test('video controls preserve ordered references and prevent unsupported combinations',async({page},info)=>{
  const submissions: Record<string,unknown>[]=[]
  await page.route('**/creative-media-capabilities',route=>route.fulfill({json:{video:{provider:'gemini-video',model:'gemini-omni-1.1-flash',mode:'real',ready:true}}}))
  await page.route('**/projects/story-1/media?*',route=>route.fulfill({json:{version:1,assets:[...images,{id:'motion',name:'动作参考',kind:'video',role:'shot',shot_id:'',file_url:'',duration:3}],exports:[],timeline:{clips:[],music_volume:.2,music_asset_id:''}}}))
  await page.route('**/projects/story-1/creative-controls?*',route=>route.fulfill({json:new URL(route.request().url()).searchParams.get('kind')==='video'?{max_references:4,frames:true,motion_reference:true}:{max_references:0,frames:false,motion_reference:false}}))
  await page.route('**/projects/story-1/creative-jobs?*',route=>{
    if(route.request().method()==='POST') {submissions.push(route.request().postDataJSON());return route.fulfill({json:{id:'job-1',status:'queued'}})}
    return route.fulfill({json:[]})
  })
  await page.goto('/projects/story-1/production')
  await page.getByLabel('制作内容',{exact:true}).selectOption('video')
  await page.getByText('多参考与帧控制',{exact:true}).click()
  await page.getByLabel('尾帧图片',{exact:true}).selectOption('image-1')
  await page.getByLabel('创作或修改要求',{exact:true}).fill('人物走进雨后的街道，保持衣服和光线一致')
  await expect(page.getByRole('button',{name:'生成候选',exact:true})).toBeDisabled()
  await page.getByLabel('首帧图片',{exact:true}).selectOption('image-0')
  await page.getByRole('checkbox',{name:'参考C',exact:true}).check()
  await page.getByRole('checkbox',{name:'参考A',exact:true}).check()
  await page.getByLabel('动作与外观参考视频',{exact:true}).selectOption('motion')
  await expect(page.getByLabel('参考画面',{exact:true})).toBeDisabled()
  await page.getByRole('button',{name:'生成候选',exact:true}).click()
  await expect.poll(()=>submissions.length).toBe(1)
  expect(submissions[0]).toMatchObject({first_frame_asset_id:'image-0',last_frame_asset_id:'image-1',reference_asset_ids:['image-2','image-0'],motion_reference_asset_id:'motion'})
  await page.getByLabel('制作内容',{exact:true}).selectOption('image')
  await expect(page.getByText('当前模型尚未提供这些控制。',{exact:true})).toBeVisible()
  await expect(page.getByLabel('首帧图片',{exact:true})).toHaveCount(0)
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.screenshot({path:info.outputPath('reference-controls.png'),fullPage:true})
})

test('episode switching protects drafts and saves independent timelines and export requests',async({page},info)=>{
  const blank={clips:[] as Record<string,unknown>[],music_volume:.2,music_asset_id:''}
  const media={version:1,assets:images,exports:[],timeline:structuredClone(blank),episode_timelines:{} as Record<string,typeof blank>}
  const exports:Record<string,unknown>[]=[]
  await page.route('**/projects/story-1/media?*',route=>route.fulfill({json:media}))
  await page.route('**/projects/story-1/media/timeline?*',route=>{
    const body=route.request().postDataJSON()
    expect(body.expected_version).toBe(media.version)
    if(body.timeline.episode_number>1) media.episode_timelines[String(body.timeline.episode_number)]=body.timeline
    else media.timeline=body.timeline
    media.version++
    return route.fulfill({json:media})
  })
  await page.route('**/projects/story-1/media/exports?*',route=>{exports.push(route.request().postDataJSON());return route.fulfill({json:{id:'export-2',status:'queued',episode_number:2}})})
  await page.goto('/projects/story-1/media')
  await page.getByRole('button',{name:'加入时间轴',exact:true}).first().click()
  await page.getByLabel('片段 1 时长',{exact:true}).fill('4')
  await page.getByRole('button',{name:'进入该集',exact:true}).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.getByRole('button',{name:'继续编辑',exact:true}).click()
  await expect(page.getByLabel('当前剪辑集数',{exact:true})).toHaveValue('1')
  await expect(page.getByLabel('片段 1 时长',{exact:true})).toHaveValue('4')
  await page.getByRole('button',{name:'保存时间轴',exact:true}).click()
  await expect(page.getByRole('button',{name:'保存时间轴',exact:true})).toBeDisabled()
  await page.getByRole('button',{name:'进入该集',exact:true}).click()
  await expect(page.getByLabel('当前剪辑集数',{exact:true})).toHaveValue('2')
  await expect(page.locator('.media-timeline > article')).toHaveCount(0)
  await page.getByRole('button',{name:'加入时间轴',exact:true}).nth(1).click()
  await page.getByLabel('片段 1 时长',{exact:true}).fill('6')
  await page.getByRole('button',{name:'保存时间轴',exact:true}).click()
  await expect(page.getByRole('button',{name:'保存时间轴',exact:true})).toBeDisabled()
  await page.getByRole('button',{name:'合成并导出 MP4',exact:true}).click()
  await expect.poll(()=>exports.length).toBe(1)
  expect(exports[0]).toEqual({expected_version:3,episode_number:2})
  await page.reload()
  await expect(page.getByLabel('片段 1 时长',{exact:true})).toHaveValue('4')
  await page.getByLabel('当前剪辑集数',{exact:true}).selectOption('2')
  await expect(page.getByLabel('片段 1 时长',{exact:true})).toHaveValue('6')
  expect(media.timeline.clips[0].asset_id).toBe('image-0')
  expect(media.episode_timelines['2'].clips[0].asset_id).toBe('image-1')
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.screenshot({path:info.outputPath('episode-editing.png'),fullPage:true})
})
