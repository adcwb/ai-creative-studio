import {test, expect} from '@playwright/test'

const fixture='http://127.0.0.1:5189'
test('clearing history confirms once and preserves running tasks and other workspaces',async({page,request},info)=>{
 await request.post(fixture+'/__test__/reset',{data:{}})
 const [base]=await(await request.get(fixture+'/api/v1/tasks?space_id=personal-demo')).json()
 const tasks=[{...base,id:'completed',result_width:1536,result_height:2048},{...base,id:'active',status:'running',result_url:undefined},{...base,id:'foreign',space_id:'team-demo'}]
 await request.post(fixture+'/__test__/reset',{data:{tasks}})
 await page.goto('/studio')
 await expect(page.locator('.task')).toHaveCount(2)
 await expect(page.locator('.task-resolution').filter({hasText:'1536 × 2048 px'})).toBeVisible()
 const preview=page.locator('.task').filter({hasText:'1536 × 2048 px'}).locator('.result-preview')
 await expect.poll(()=>preview.evaluate(button=>{const image=button.querySelector('img')!.getBoundingClientRect(),frame=button.getBoundingClientRect();return image.height>=190&&frame.height>=image.height-1})).toBe(true)
 await preview.screenshot({path:info.outputPath('unclipped-preview.png')})
 await expect(page.locator('.task').filter({hasText:'画面正在准备中'}).getByRole('button',{name:'删除创作记录'})).toHaveCount(0)
 await page.getByRole('button',{name:'清空历史',exact:true}).click()
 const confirm=page.getByRole('dialog',{name:'清空创作历史？'})
 await confirm.getByRole('button',{name:'取消',exact:true}).click()
 await expect(page.locator('.task')).toHaveCount(2)
 await page.getByRole('button',{name:'清空历史',exact:true}).click()
 await confirm.getByRole('button',{name:'确认清空',exact:true}).click()
 await expect(confirm).toHaveCount(0)
 await expect(page.locator('.task')).toHaveCount(1)
 expect(await(await request.get(fixture+'/api/v1/tasks?space_id=team-demo')).json()).toHaveLength(1)
 await page.reload()
 await expect(page.locator('.task')).toHaveCount(1)
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
})
