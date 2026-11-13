import {readSeriesRecovery} from '../series/useSeriesWrite'
export function restoreSourceKey(actor:string,space:string,project:string){return `studio.revision.restore-source:${actor}:${space}:${project}`}
export function pendingRestoreRevision(actor:string,space:string,project:string){
 try{const id=sessionStorage.getItem(restoreSourceKey(actor,space,project))??'';if(!/^(main|[a-f0-9]{32})_[0-9]{20}_[a-f0-9]{64}$/.test(id))return '';return readSeriesRecovery(actor,space,`revision:restore:${project}`,`/projects/${project}/revisions/${id}/restore-timeline`,'POST')?id:''}catch{return ''}
}
