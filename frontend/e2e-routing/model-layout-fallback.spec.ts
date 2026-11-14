import {test,expect} from '@playwright/test'

test('English model bindings contain their table across fallback fonts and viewport changes',async({page,request},info)=>{
 await request.post('http://127.0.0.1:5189/__test__/reset',{data:{management:{version:1,secret_storage_available:true,governance_ready:true,legacy_models:0,
  gateways:[{id:'gateway-1',name:'本地协议网关',base_url:'https://gateway.example.invalid/v1',enabled:true,version:1,timeout_seconds:30,secret_status:'encrypted'}],
  models:[{id:'model-1',gateway_id:'gateway-1',name:'手工声明的测试模型',model:'synthetic-image',capabilities:['text','vision','image','image_edit','video','speech','asr','asr_timestamps'],enabled:true,version:1,max_output_tokens:512,json_output:true,video_seconds:4,video_size:'1280x720'}],
  bindings:[{feature_key:'image_generation',model_config_id:'model-1',enabled:true,allow_override:true,version:1}],
  features:[['prompt_optimization','提示词优化','text'],['workflow_text','流程文本处理','text'],['brief_analysis','创意分析','text'],['script_generation','剧本生成','text'],['storyboard_planning','分镜规划','text'],['story_continuity','故事连续性检查','text'],['image_generation','图像创作','image'],['character_image','角色参考图','image'],['shot_image','镜头图片','image'],['image_edit','图片编辑','image_edit'],['shot_video','镜头视频','video'],['speech_synthesis','配音','speech'],['speech_transcription','语音转写','asr'],['subtitle_alignment','字幕识别','asr_timestamps'],['vision_analysis','图片分析','vision'],['consistency_check','跨镜头一致性检查','vision']].map(([key,name,capability])=>({key,name,capability}))
 }}})
 await page.addInitScript(()=>localStorage.setItem('studio.locale','en'))
 const desktop = info.project.name === 'desktop'
 const initial = desktop ? {width:1920,height:1080} : {width:760,height:844}
 const narrow = desktop ? {width:1366,height:768} : {width:390,height:844}
 await page.setViewportSize(initial)
 await page.goto('/settings/models')
 await expect(page.locator('#model-panel-bindings')).toBeVisible()
 await expect(page.locator('.feature-binding-table tbody tr')).toHaveCount(16)
 await page.screenshot({path:info.outputPath('models-initial.png'),fullPage:true})
 for(const font of ['Arial','DejaVu Sans','Liberation Sans','Noto Sans CJK SC']){
  const style=await page.addStyleTag({content:`.user-workspace,.user-workspace * {font-family:"${font}",sans-serif!important}`})
  await page.setViewportSize(narrow)
  await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
  const geometry=await page.evaluate(()=>({viewport:innerWidth,width:document.documentElement.scrollWidth,overflow:[...document.querySelectorAll<HTMLElement>('.page-content *')].filter(n=>n.getBoundingClientRect().right>innerWidth+1&&n.getClientRects().length).slice(0,18).map(n=>({tag:n.tagName,class:n.className,width:n.getBoundingClientRect().width,right:n.getBoundingClientRect().right,minWidth:getComputedStyle(n).minWidth,overflow:getComputedStyle(n).overflowX}))}))
  expect(geometry.width,JSON.stringify({font,...geometry})).toBeLessThanOrEqual(geometry.viewport)
  await expect(page.locator('#model-panel-bindings .table-scroll')).toBeInViewport()
  // Horizontal overflow belongs to the desktop table, never the document.
  // A phone renders cards; either layout must keep the final action reachable.
  const action = page.locator('.feature-binding-table tbody tr').last().getByRole('button', {name:'Save routing',exact:true})
  await action.scrollIntoViewIfNeeded()
  await expect(action).toBeInViewport()
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await page.screenshot({path:info.outputPath(`models-${font.replaceAll(' ','-')}.png`),fullPage:true})
  await style.evaluate(n=>n.remove());await page.setViewportSize(initial)
 }
})
