declare module '@met4citizen/talkinghead' {
 export class TalkingHead {
  constructor(node:HTMLElement,options:Record<string,unknown>)
  scene:unknown
  camera:unknown
  renderer:{render:(scene:unknown,camera:unknown)=>void}|null
  audioCtx:AudioContext
  isSpeaking:boolean
  isAudioPlaying:boolean
  showAvatar(avatar:{url:string;body:string;avatarMood:string;lipsyncLang:string},progress?:(event:ProgressEvent)=>void,preprocess?:(gltf:{scene:{traverse:(visit:(node:{name:string;isBone?:boolean})=>void)=>void}})=>void):Promise<void>
  speakAudio(audio:{audio:AudioBuffer;anim:{name:string;dt:number[];vs:{jawOpen:number[]}}},options?:{isRaw:boolean}):void
  setFixedValue(name:string,value:number|null,ms?:number):void
  start():void
  stop():void
  stopSpeaking():void
  dispose():void
 }
}
