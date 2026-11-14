// Language-independent, measured audio energy. This is not phoneme alignment.
export function audioJawMotion(audio:Pick<AudioBuffer,'sampleRate'|'length'|'numberOfChannels'|'getChannelData'>){
 if(!Number.isFinite(audio.sampleRate)||audio.sampleRate<=0||audio.length<1||audio.numberOfChannels<1)throw new Error('Invalid decoded audio')
 const step=Math.max(1,Math.round(audio.sampleRate*.04)),dt=[0],jawOpen=[0]
 const channels=Array.from({length:audio.numberOfChannels},(_,n)=>audio.getChannelData(n))
 let previous=0
 for(let start=0;start<audio.length;start+=step){
  const end=Math.min(start+step,audio.length);let energy=0
  for(const samples of channels)for(let i=start;i<end;i++){if(!Number.isFinite(samples[i]))throw new Error('Invalid decoded audio');energy+=samples[i]*samples[i]}
  const rms=Math.sqrt(energy/Math.max(1,(end-start)*channels.length)),value=rms<.008?0:Math.min(.65,Math.sqrt(rms)*1.25)
  dt.push((end-start)/audio.sampleRate*1000);previous=.3*previous+.7*value;jawOpen.push(previous)
 }
 dt.push(0);jawOpen.push(0)
 return {name:'blendshapes',dt,vs:{jawOpen}}
}
