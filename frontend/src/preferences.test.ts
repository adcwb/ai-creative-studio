import {afterEach, expect, test, vi} from 'vitest'
import {preferenceKey, readTheme} from './preferences'
afterEach(() => vi.unstubAllGlobals())
test('preference keys isolate users, and missing or invalid storage fails to forest', () => {
 const a=preferenceKey('user-a'), b=preferenceKey('user-b')
 expect(a).not.toBe(b)
 const values=new Map([[a,'screening'],[b,'paper']])
 vi.stubGlobal('localStorage',{getItem:(key:string)=>values.get(key) ?? null})
 expect(readTheme(a)).toBe('screening'); expect(readTheme(b)).toBe('paper')
 expect(readTheme(preferenceKey('new-user'))).toBe('forest')
 values.set(a,'unsupported'); expect(readTheme(a)).toBe('forest')
})
test('unavailable browser storage does not prevent opening the workspace', () => {
 vi.stubGlobal('localStorage',{getItem:()=>{throw new Error('blocked')}})
 expect(readTheme(preferenceKey('user'))).toBe('forest')
})
