import {describe,it,expect} from 'vitest'
import {adoptedPreviewChoices,eligiblePreviewShots,type PreviewMedia,type PreviewPlan} from './animatic'
const shot={id:'one',scene_index:1,description:'saved shot',shot_size:'medium' as const,camera_movement:'static' as const,duration_seconds:121,dialogue:''}
const project:PreviewPlan={id:'project',version:3,script_version:2,storyboard:{source_script_version:1,shots:[shot,{...shot,id:'seven',scene_index:2},{...shot,id:'free',scene_index:0}]}}
const media:PreviewMedia={version:2,assets:[{id:'old',kind:'image',name:'old.png',file_url:'',duration:0,role:'shot'},{id:'new',kind:'image',name:'new.png',file_url:'',duration:0,role:'shot'},{id:'video',kind:'video',name:'video.mp4',file_url:'',duration:4,role:'shot'}],selections:[{shot_id:'one',asset_id:'old'},{shot_id:'one',asset_id:'new'},{shot_id:'seven',asset_id:'video'},{shot_id:'free',asset_id:'old'}]}
describe('animatic source planning',()=>{
 it('uses the exact saved-source episode map instead of current scene order or display names',()=>{
  expect(eligiblePreviewShots(project,1,{one:1,seven:7,free:0}).map(v=>v.id)).toEqual(['one','free'])
  expect(eligiblePreviewShots(project,7,{one:1,seven:7,free:0}).map(v=>v.id)).toEqual(['seven','free'])
  expect(eligiblePreviewShots(project,1,{one:-1,seven:7,free:0}).map(v=>v.id)).toEqual(['free'])
 })
 it('prefills adopted stills without borrowing a video or assigning an ambiguous legacy shot silently',()=>{
  expect(adoptedPreviewChoices(project,media,1,{one:1,seven:7,free:0})).toEqual([{shot_id:'one',asset_id:'new',duration:121}])
  expect(adoptedPreviewChoices(project,media,7,{one:1,seven:7,free:0})).toEqual([])
  expect(adoptedPreviewChoices({...project,script_version:0},media,1,{one:0,seven:0,free:0}).map(v=>v.shot_id)).toEqual(['one','free'])
 })
})
