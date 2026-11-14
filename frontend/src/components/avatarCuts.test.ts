import {describe,it,expect} from 'vitest'
import {avatarSplitInput,defaultAvatarSplit} from './avatarCuts'
describe('avatar cut form',()=>{
 it('accepts localized comma/space cut times and carries an explicitly pinned preview',()=>{
  expect(avatarSplitInput({mode:'manual',manual:'12.125， 27.5  43',approved:'a'.repeat(64)})).toEqual({valid:true,input:{avatar_split_mode:'manual',avatar_cut_points:[12.125,27.5,43],avatar_plan_sha256:'a'.repeat(64)}})
 })
 it('rejects unordered, non-finite or non-positive manual input before dispatch',()=>{
  for(const manual of ['12, 10','12,12','0,12','NaN','Infinity','oops'])expect(avatarSplitInput({mode:'manual',manual}).valid).toBe(false)
 })
 it('does not accidentally carry manual cuts into acoustic or fixed plans',()=>{
  expect(avatarSplitInput({...defaultAvatarSplit,manual:'12,10'})).toEqual({valid:true,input:{avatar_split_mode:'pause'}})
 })
})
