import {it,expect} from 'vitest'
import {audioJawMotion} from './audioMotion'
it('uses stereo energy without cancelling opposite channels and ends closed at the original duration',()=>{
 const a=new Float32Array(4800).fill(.2),b=new Float32Array(4800).fill(-.2)
 const motion=audioJawMotion({sampleRate:48000,length:4800,numberOfChannels:2,getChannelData:n=>n?a:b})
 expect(motion.vs.jawOpen.slice(1,-1).some(v=>v>0)).toBe(true)
 expect(motion.vs.jawOpen.at(-1)).toBe(0);expect(motion.dt.reduce((a,b)=>a+b,0)).toBeCloseTo(100,8)
})
it('keeps digital silence closed',()=>{
 const motion=audioJawMotion({sampleRate:16000,length:1600,numberOfChannels:1,getChannelData:()=>new Float32Array(1600)})
 expect(motion.vs.jawOpen.every(v=>v===0)).toBe(true)
})

it('rejects non-finite decoded samples instead of animating NaN',()=>{
 expect(()=>audioJawMotion({sampleRate:16000,length:1,numberOfChannels:1,getChannelData:()=>new Float32Array([NaN])})).toThrow()
})
