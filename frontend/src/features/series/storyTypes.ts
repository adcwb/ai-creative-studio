export type StoryKind='bible'|'fact'|'event'|'foreshadow'|'cast'
export interface StoryCharacter {id:string;name:string;desire:string;flaw:string;arc:string;relationships:string;visual_intent:string;voice_intent:string}
export interface StoryBible {theme:string;world_rules:string;story_arc:string;visual_intent:string;immutable_rules:string;characters:StoryCharacter[]}
export interface StoryFact {subject:string;predicate:string;value:string;perspective:'objective'|'subjective'|'unconfirmed';believer_id?:string;story_time:string;audience_from_episode_id?:string;known_by:{character_id:string;from_episode_id:string}[]}
export interface StoryEvent {story_time:string;episode_id:string;description:string;state_change:string}
export interface StoryForeshadow {setup_episode_id:string;payoff_episode_id?:string;intent:string;state:'planned'|'planted'|'paid_off'|'abandoned'}
export interface StoryBody {cast?:StoryCast;bible?:StoryBible;fact?:StoryFact;event?:StoryEvent;foreshadow?:StoryForeshadow;from_episode_id?:string;to_episode_id?:string;evidence:{episode_id:string;scene_id:string;script_version:number;script_sha256:string;quote:string}[]}
export interface StorySummary {id:string;series_id:string;version_id:string;number:number;title:string;kind:StoryKind;status:'active'|'archived';required:boolean;content_sha256:string}
export interface StoryVersion extends StorySummary {space_id:string;body:StoryBody;created_by:string;created_at:string}
export interface StoryInput {expected_version:number;title:string;kind:StoryKind;status:'active'|'archived';required:boolean;body:StoryBody}
export interface StoryEpisode {id:string;season_id:string;season_ordinal:number;ordinal:number;title:string}
export interface StoryPage {items:StorySummary[];next_cursor:string}
export interface StoryHistory {items:StoryVersion[];next_cursor:string}
export interface StoryPreview {episode_id:string;sources:StorySummary[];included:(StorySummary&{body:StoryBody;audience_knows:boolean;known_by:string[]})[];omitted:{id:string;version_id:string;reason:string}[];problems:{code:string;record_id?:string;message:string}[];utf8_bytes:number;snapshot_sha256:string}
export const storyLabels:Record<StoryKind,string>={bible:'故事圣经',fact:'事实与认知',event:'故事事件',foreshadow:'伏笔与回收',cast:'本集选角'}
export function emptyBody(kind:StoryKind):StoryBody {const evidence:StoryBody['evidence']=[];switch(kind){case'bible':return{evidence,bible:{theme:'',world_rules:'',story_arc:'',visual_intent:'',immutable_rules:'',characters:[]}};case'fact':return{evidence,fact:{subject:'',predicate:'',value:'',perspective:'objective',story_time:'',known_by:[]}};case'event':return{evidence,event:{episode_id:'',story_time:'',description:'',state_change:''}};case'cast':return{evidence,cast:{episode_id:'',script_version:0,script_sha256:'',bible_version_id:'',characters:[]}};case'foreshadow':return{evidence,foreshadow:{setup_episode_id:'',intent:'',state:'planned'}}}}

export interface StoryContextSelection {query:string;record_ids:string[]}
export interface StoryContextSummary {id:string;episode_id:string;script_version:number;created_at:string;included_count:number;problem_count:number;content_sha256:string}
export interface StoryContextSnapshot {series_id:string;id:string;episode_id:string;script_version:number;created_at:string;content_sha256:string;context:Omit<StoryPreview,'snapshot_sha256'>&{selection:StoryContextSelection};script:null|{title:string;scenes:{id:string;heading:string;action:string;dialogue:{character:string;text:string}[]}[]}}
export interface StoryContextFreshness {snapshot_id:string;current:boolean;directory_changed:boolean;project_changed:boolean;script_changed:boolean;material_changed:boolean}

export interface StoryCheckEvidence {record_id?:string;version_id?:string;scene_id?:string;quote:string}
export interface StoryFinding {id:string;title:string;kind:string;severity:string;confidence:string;explanation:string;evidence:StoryCheckEvidence[];target_record_id?:string;proposal?:StoryInput}
export interface StoryCheckSummary {id:string;episode_id:string;context_snapshot_id:string;state:string;model:string;created_at:string;error_code?:string}
export interface StoryCheck {id:string;episode_id:string;input:{context_snapshot_id:string;model_config_id:string;model_config_version:number};state:string;model:string;error_code?:string;result?:{summary:string;findings:StoryFinding[]}}

export interface StoryDecision {id:string;episode_id:string;check_id:string;finding_id:string;action:'adopt'|'reject'|'intentional';reason:string;review_sha256:string;context_snapshot_id:string;target_record_id?:string;version_id?:string;created_by:string;created_at:string}
export interface StoryFindingReview {check_id:string;finding:StoryFinding;snapshot:StoryContextSnapshot;current_target?:StoryVersion;current_bible?:StoryVersion;review_sha256:string;sources_changed:boolean;current_script:StoryContextSnapshot["script"];current_script_version:number;script_changed:boolean;project_changed:boolean;directory_changed:boolean;changed_sources:{previous?:StorySummary;current?:StorySummary}[];decision?:StoryDecision}
export interface StoryDecisionInput {action:'adopt'|'reject'|'intentional';reason:string;review_sha256:string;acknowledge_changed:boolean;proposal?:StoryInput}

export interface StoryCastCharacter {script_character_id:string;script_character_name:string;series_character_id:string;series_character_name:string;library_asset_id?:string;asset_version_id?:string;asset_name?:string;notes:string}
export interface StoryCast {episode_id:string;script_version:number;script_sha256:string;bible_version_id:string;characters:StoryCastCharacter[]}
export interface StoryCastOptions {episode_id:string;series_id:string;script:null|{characters:{id:string;name:string;description:string}[]};script_version:number;script_sha256:string;bible:StoryVersion|null;current:StoryVersion|null;problems:{code:string;message:string}[]}
