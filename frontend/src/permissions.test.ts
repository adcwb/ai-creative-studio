import {describe,it,expect} from 'vitest'
import {hasPermission} from './permissions'
import type {Space} from './api'
const space=(role:Space['role'],permissions?:string[]):Space=>({id:'test',name:'Test team',type:'team',owner_id:'owner',role,permissions})
describe('effective workspace permissions',()=>{
 it('respects explicitly revoked grants even for owners',()=>{expect(hasPermission(space('owner',[]),'models.manage')).toBe(false);expect(hasPermission(space('editor',['workspace.write']),'models.execute')).toBe(false)})
 it('retains known role compatibility with older servers and denies unknown grants',()=>{expect(hasPermission(space('editor'),'models.execute')).toBe(true);expect(hasPermission(space('viewer'),'workspace.write')).toBe(false);expect(hasPermission(space(undefined),'models.manage')).toBe(false);expect(hasPermission(space('owner'),'unknown')).toBe(false)})
})
