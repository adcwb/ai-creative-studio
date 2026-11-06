import type {StoryboardShot} from './drama'
export const directorFieldLabels = {purpose:'叙事目的',performance:'表演要求',continuity:'连续性要求',angle:'机位与角度',composition:'构图与留白',axis:'轴线与空间关系',eyeline:'视线与视点',blocking:'站位与动作',lighting:'光线与色彩',sound:'对白、环境音与音乐意图'} as const
export const directorControlLabels = {...directorFieldLabels,shot_size:'景别',camera_movement:'运镜',duration_seconds:'镜头时长意图',image_reference:'参考画面',first_frame:'首帧图片',last_frame:'尾帧图片',motion_reference:'动作与外观参考视频',native_voice:'原生音色'} as const
export const directorModeLabels = {prompt_only:'仅提示词意图',native:'原生参数',reference:'参考输入',unsupported:'当前不支持'} as const
export type DirectorMode = keyof typeof directorModeLabels
export interface DirectorSnapshot {omitted_neighbours?:string[];project_version:number;script_version:number;sha256:string;kind:string;current:StoryboardShot;previous?:StoryboardShot;next?:StoryboardShot;omitted_fields:string[]}
export function directorValues(shot:StoryboardShot){return Object.entries(directorFieldLabels).filter(([key])=>Boolean(shot[key as keyof typeof directorFieldLabels]?.trim()))}
