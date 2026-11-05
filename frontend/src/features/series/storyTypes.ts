export type StoryKind='bible'|'fact'|'event'|'foreshadow'
export interface StoryCharacter {id:string;name:string;desire:string;flaw:string;arc:string;relationships:string;visual_intent:string;voice_intent:string}
export interface StoryBible {theme:string;world_rules:string;story_arc:string;visual_intent:string;immutable_rules:string;characters:StoryCharacter[]}
export interface StoryFact {subject:string;predicate:string;value:string;perspective:'objective'|'subjective'|'unconfirmed';believer_id?:string;story_time:string;audience_from_episode_id?:string;known_by:{character_id:string;from_episode_id:string}[]}
export interface StoryEvent {story_time:string;episode_id:string;description:string;state_change:string}
export interface StoryForeshadow {setup_episode_id:string;payoff_episode_id?:string;intent:string;state:'planned'|'planted'|'paid_off'|'abandoned'}
export interface StoryBody {bible?:StoryBible;fact?:StoryFact;event?:StoryEvent;foreshadow?:StoryForeshadow;from_episode_id?:string;to_episode_id?:string;evidence:{episode_id:string;scene_id:string;script_version:number;script_sha256:string;quote:string}[]}
export interface StorySummary {id:string;series_id:string;version_id:string;number:number;title:string;kind:StoryKind;status:'active'|'archived';required:boolean;content_sha256:string}
export interface StoryVersion extends StorySummary {space_id:string;body:StoryBody;created_by:string;created_at:string}
export interface StoryInput {expected_version:number;title:string;kind:StoryKind;status:'active'|'archived';required:boolean;body:StoryBody}
export interface StoryEpisode {id:string;season_id:string;season_ordinal:number;ordinal:number;title:string}
export interface StoryPage {items:StorySummary[];next_cursor:string}
export interface StoryHistory {items:StoryVersion[];next_cursor:string}
export interface StoryPreview {episode_id:string;sources:StorySummary[];included:(StorySummary&{body:StoryBody;audience_knows:boolean;known_by:string[]})[];omitted:{id:string;version_id:string;reason:string}[];problems:{code:string;record_id?:string;message:string}[];utf8_bytes:number;snapshot_sha256:string}
export const storyLabels:Record<StoryKind,string>={bible:'故事圣经',fact:'事实与认知',event:'故事事件',foreshadow:'伏笔与回收'}
export function emptyBody(kind:StoryKind):StoryBody {const evidence:StoryBody['evidence']=[];switch(kind){case'bible':return{evidence,bible:{theme:'',world_rules:'',story_arc:'',visual_intent:'',immutable_rules:'',characters:[]}};case'fact':return{evidence,fact:{subject:'',predicate:'',value:'',perspective:'objective',story_time:'',known_by:[]}};case'event':return{evidence,event:{episode_id:'',story_time:'',description:'',state_change:''}};case'foreshadow':return{evidence,foreshadow:{setup_episode_id:'',intent:'',state:'planned'}}}}
