import {test,expect} from '@playwright/test'
import {execFileSync} from 'node:child_process'
import {mkdtempSync,readFileSync,rmSync} from 'node:fs'
import {tmpdir} from 'node:os'
import {join} from 'node:path'

test('fixed avatar output has manual review history, exact recovery and explicit cloud transfer',async({page,request},info)=>{
 if(info.project.name==='desktop')await page.setViewportSize({width:1920,height:1080})
 await request.post('http://127.0.0.1:5189/__test__/reset',{data:{}})
 const temp=mkdtempSync(join(tmpdir(),'avatar-ui-review-'));let video:Buffer,photo:Buffer
 try{const file=join(temp,'clip.mp4');execFileSync('ffmpeg',['-hide_banner','-loglevel','error','-f','lavfi','-i','color=c=teal:s=320x320:r=12:d=1','-an','-c:v','libx264','-pix_fmt','yuv420p','-movflags','+faststart',file]);video=readFileSync(file);const still=join(temp,'reference.png');execFileSync('ffmpeg',['-hide_banner','-loglevel','error','-f','lavfi','-i','color=c=skyblue:s=400x400','-frames:v','1',still]);photo=readFileSync(still)}finally{rmSync(temp,{recursive:true,force:true})}
 const speech=Buffer.alloc(44+16000);speech.write('RIFF');speech.writeUInt32LE(speech.length-8,4);speech.write('WAVEfmt ',8);speech.writeUInt32LE(16,16);speech.writeUInt16LE(1,20);speech.writeUInt16LE(1,22);speech.writeUInt32LE(8000,24);speech.writeUInt32LE(16000,28);speech.writeUInt16LE(2,32);speech.writeUInt16LE(16,34);speech.write('data',36);speech.writeUInt32LE(16000,40)
 await page.route('**/avatar-review-speech.wav',r=>r.fulfill({body:speech,contentType:'audio/wav'}))
 const reviews:Record<string,unknown>[]=[]
 const output={id:'review-output',kind:'digital_human_authorized',status:'succeeded',asset_id:'output',avatar_id:'person',reference_asset_id:'photo',audio_asset_id:'audio',avatar_reviews:reviews}
 const pending={id:'cloud-pending',kind:'digital_human_authorized',status:'failed',avatar_recovery_phase:'remote_pending'}
 await page.route('**/avatar-review-video.mp4',r=>r.fulfill({body:video,contentType:'video/mp4'}))
 await page.route('**/avatar-review-photo.png',r=>r.fulfill({body:photo,contentType:'image/png'}))
 await page.route('**/api/v1/creative-media-capabilities',r=>r.fulfill({json:{digital_human_photo:{provider:'dashscope-s2v',model:'wan2.2-s2v',ready:true,message:'Synthetic dedicated cloud protocol'}}}))
 await page.route('**/api/v1/projects/story-1/digital-humans?*',r=>r.fulfill({json:[{id:'person',name:'Synthetic likeness',reference_asset_id:'photo',route:'photo',consent_reference:'likeness-consent'}]}))
 await page.route('**/api/v1/projects/story-1/voice-authorizations?*',r=>r.fulfill({json:[{id:'voice',audio_asset_id:'audio',consent_reference:'voice-consent'}]}))
 await page.route('**/api/v1/projects/story-1/media?*',r=>r.fulfill({json:{assets:[{id:'photo',name:'Synthetic reference',kind:'image',duration:0,file_url:'/avatar-review-photo.png'},{id:'audio',name:'Synthetic speech',kind:'audio',duration:1,file_url:'/avatar-review-speech.wav'},{id:'output',name:'Synthetic decoded clip',kind:'video',duration:1,file_url:'/avatar-review-video.mp4'}]}}))
 await page.route('**/api/v1/projects/story-1/creative-jobs?*',r=>r.fulfill({json:[output,pending]}))
 const attempts:{key:string|undefined;body:Record<string,unknown>}[]=[]
 await page.route('**/api/v1/projects/story-1/creative-jobs/review-output/avatar-reviews?*',async r=>{
  const body=r.request().postDataJSON();attempts.push({key:r.request().headers()['idempotency-key'],body})
  if(attempts.length===1){expect(body).toMatchObject({expected_version:0,asset_id:'output',outcome:'adopted',identity_confirmed:true,lip_sync_confirmed:true,audio_confirmed:true,disclosure_confirmed:true});reviews.push({id:'review-1',version:1,...body,reviewed_by:'fixture-editor',created_at:new Date().toISOString()});await r.abort('failed');return}
  if(attempts.length===2){expect(attempts[1]).toEqual(attempts[0]);await r.fulfill({json:reviews[0]});return}
  expect(body).toMatchObject({expected_version:1,asset_id:'output',outcome:'rework',note:'请调整第二句话口型'});const review={id:'review-2',version:2,...body,reviewed_by:'fixture-editor',created_at:new Date().toISOString()};reviews.push(review);await r.fulfill({json:review})
 })
 await page.goto('/projects/story-1/avatar')
 const panel=page.getByRole('region',{name:'数字人成片审阅',exact:true})
 await expect(panel).toBeVisible();await expect(page.locator('.digital-human-studio>div.drama-history video')).toHaveJSProperty('readyState',4)
 await expect(panel.getByRole('button',{name:'保存人工审片记录',exact:true})).toBeDisabled()
 for(const label of ['人物与参考形象一致','口型与配音同步','声音清晰且内容正确','已检查AI生成标识'])await panel.getByRole('checkbox',{name:label,exact:true}).check()
 await panel.getByRole('button',{name:'保存人工审片记录',exact:true}).click();await expect(panel.getByRole('button',{name:'核对原审片记录',exact:true})).toBeVisible()
 await page.reload();await panel.getByRole('button',{name:'核对原审片记录',exact:true}).click();await expect(panel.getByRole('status')).toContainText('人工确认采用')
 await panel.getByRole('combobox',{name:'审片决定',exact:true}).selectOption('rework');await expect(panel.getByRole('button',{name:'保存人工审片记录',exact:true})).toBeDisabled();await panel.getByRole('textbox',{name:'具体修改意见或审片备注',exact:true}).fill('请调整第二句话口型');await panel.getByRole('button',{name:'保存人工审片记录',exact:true}).click();await expect(panel.getByRole('status')).toContainText('需要修改')
 await panel.getByText('查看审片历史',{exact:true}).click();await expect(panel.locator('ol li')).toHaveCount(2)
 await panel.getByText('对照原始形象与配音',{exact:true}).click();await expect(panel.getByRole('img',{name:'Synthetic reference',exact:true})).toHaveJSProperty('naturalWidth',400);await panel.locator('audio').evaluate((a:HTMLAudioElement)=>{a.preload='metadata';a.load()});await expect(panel.locator('audio')).toHaveJSProperty('duration',1)
 await expect(page.getByText('恢复只查询原百炼任务并取回结果，不再次提交生成；仍检查当前授权。',{exact:true})).toBeVisible();await expect(page.getByRole('button',{name:'核对原云端口播任务',exact:true})).toBeVisible()
 await page.getByRole('combobox',{name:'已授权形象',exact:true}).selectOption('person');await page.getByRole('combobox',{name:'选择口播音频',exact:true}).selectOption('audio');await page.getByRole('combobox',{name:'已保存声音授权',exact:true}).selectOption('voice')
 await expect(page.getByRole('button',{name:'生成数字人口播',exact:true})).toBeDisabled();await page.getByRole('checkbox',{name:'我同意将所选形象与配音临时发送到部署方配置的私有对象存储及百炼数字人服务',exact:true}).check();await expect(page.getByRole('button',{name:'生成数字人口播',exact:true})).toBeEnabled()
 expect(attempts).toHaveLength(3);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1)).toBe(true)
 await panel.scrollIntoViewIfNeeded();await page.screenshot({path:`/tmp/avatar-cloud-review-${info.project.name}.png`,fullPage:true})
})
