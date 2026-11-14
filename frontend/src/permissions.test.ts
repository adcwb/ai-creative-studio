import {describe,it,expect} from 'vitest'
import {hasPermission} from './permissions'
import type {Space} from './api'
const space=(role:Space['role'],permissions?:string[]):Space=>({id:'test',name:'Test team',type:'team',owner_id:'owner',role,permissions})
describe('effective workspace permissions',()=>{
 it('respects explicitly revoked grants even for owners',()=>{expect(hasPermission(space('owner',[]),'models.manage')).toBe(false);expect(hasPermission(space('editor',['workspace.write']),'models.execute')).toBe(false)})
 it('retains known role compatibility with older servers and denies unknown grants',()=>{expect(hasPermission(space('editor'),'models.execute')).toBe(true);expect(hasPermission(space('viewer'),'workspace.write')).toBe(false);expect(hasPermission(space(undefined),'models.manage')).toBe(false);expect(hasPermission(space('owner'),'unknown')).toBe(false)})
})

it('allows legacy demo editing only for explicit demo IDs while custom grants remain authoritative',()=>{
 const demo={id:'personal-demo',name:'Demo',type:'personal',owner_id:''}
 expect(hasPermission(demo,'workspace.write')).toBe(true)
 expect(hasPermission({...demo,permissions:[]},'workspace.write')).toBe(false)
 expect(hasPermission({...demo,id:'unknown'},'workspace.write')).toBe(false)
 expect(hasPermission({...demo,role:'editor' as const,permissions:['workspace.read']},'models.execute')).toBe(false)
})
