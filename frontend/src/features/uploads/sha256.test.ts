import {createHash,randomBytes} from 'node:crypto'
import {describe,it,expect} from 'vitest'
import {FileSHA256} from './sha256'
describe('incremental media fingerprints',()=>{
  it('matches native SHA-256 across padding and arbitrary chunk boundaries',()=>{
    for(const size of [0,1,55,56,63,64,65,127,128,1000,8388611]){
      const bytes=randomBytes(size),expected=createHash('sha256').update(bytes).digest('hex')
      for(const stride of size>1000 ? [65537] : [1,13,64,111]){
        const hash=new FileSHA256()
        for(let i=0;i<size;i+=stride) hash.update(bytes.subarray(i,i+stride))
        expect(hash.hex()).toBe(expected)
        expect(()=>hash.update(bytes)).toThrow()
      }
    }
  })
})
