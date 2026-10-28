import {expect,it} from 'vitest'
import {safeDouyinHandoff} from './socialDouyin'
it('accepts only the official App scheme with one bounded HTTPS media capability',()=>{
 const source='https://studio.example/api/v1/social-media/douyin?cap=synthetic-capability'
 const safe='snssdk1128://openplatform/share?image_path='+encodeURIComponent(source)
 expect(safeDouyinHandoff(safe)).toBe(true)
 for(const value of ['javascript:alert(1)',safe.replace('openplatform','evil'),safe.replace('/share?','/other?'),safe+'&video_path='+encodeURIComponent(source),'snssdk1128://openplatform/share?image_path='+encodeURIComponent('http://localhost/private'), 'snssdk1128://openplatform/share?image_path='+encodeURIComponent('https://user:secret@studio.example/api/v1/social-media/douyin?cap=x')])expect(safeDouyinHandoff(value)).toBe(false)
})
