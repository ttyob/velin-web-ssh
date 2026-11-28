import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { mkdir, readFile } from 'node:fs/promises'
import { createServer } from 'vite'
import { chromium } from 'playwright-core'
const main = await readFile(new URL('../src/main.ts', import.meta.url), 'utf8')
const styles = [...main.matchAll(/^import "([^"]+\.css)";/gm)].map(([, path]) => `import ${JSON.stringify(path.startsWith('./') ? '/src/' + path.slice(2) : path)}`).join('\n')
const entry = `${styles}
import { createApp,h } from 'vue'
import ElementPlus from 'element-plus'
import FileManager from '/src/components/FileManagerDialog.vue'
createApp({render:()=>h(FileManager,{modelValue:true,host:{id:'files-host',name:'开发服务器'},sessionId:'files-session',initialPath:'/project'})}).use(ElementPlus).mount('#app')`
const server = await createServer({root:fileURLToPath(new URL('..', import.meta.url)),server:{host:'127.0.0.1',port:0,proxy:{}},cacheDir:'node_modules/.vite-file-manager',optimizeDeps:{esbuildOptions:{target:'esnext'}},plugins:[{name:'file-manager-fixture',resolveId:id=>id==='/__files.js'?id:undefined,load:id=>id==='/__files.js'?entry:undefined,configureServer(vite){vite.middlewares.use('/__files.html',(_,response)=>{response.setHeader('Content-Type','text/html');response.end('<div id="app"></div><script type="module" src="/__files.js"></script>')})}}]})
const fs = new Map(), requests = [], writes = [], errors = []
function add(path,directory=false,content='') {fs.set(path,{name:path.split('/').at(-1),path,directory,symlink:false,size:Buffer.byteLength(content),mode:directory?'drwxr-xr-x':'-rw-r--r--',modifiedAt:'2026-11-27T02:30:00Z',content})}
add('/',true);add('/project',true);add('/project/src',true);add('/project/docs',true);add('/project/readme.md',false,'# Remote project');add('/project/config.json',false,'{"version":1}');add('/project/.env',false,'SECRET=example');add('/project/src/app.ts',false,'export const value = 1;');add('/project/docs/guide.md',false,'# Guide')
let browser
const shots=fileURLToPath(new URL('../../artifacts/file-manager/', import.meta.url));await mkdir(shots,{recursive:true})
try {
 await server.listen();browser=await chromium.launch({executablePath:'/usr/bin/chromium',args:['--no-sandbox']})
 const page=await browser.newPage({viewport:{width:1440,height:960},permissions:['clipboard-read','clipboard-write']});page.setDefaultTimeout(12000);page.on('pageerror',error=>errors.push(error.message))
 await page.route('**/api/**',async route=>{
  const request=route.request(),url=new URL(request.url()),op=url.pathname.split('/').at(-1),path=url.searchParams.get('path');let body={},status=200
  requests.push({op,path})
  try {
   if(op==='csrf') body={token:'file-fixture'}
   else {
    assert.equal(url.searchParams.get('session'),'files-session')
    const input=request.method()==='POST'?request.postDataJSON():{}
    if(op==='list') {if(!fs.has(path))throw Error('目录不存在');body={path,entries:[...fs.values()].filter(item=>item.path!==path&&item.path.slice(0,item.path.lastIndexOf('/'))===(path==='/'?'':path)).map(({content,...item})=>item)}}
    else if(op==='mkdir'){assert(!fs.has(input.path));add(input.path,true);writes.push({op,...input})}
    else if(op==='upload'){const old=fs.get(path);const content=request.postDataBuffer()?.toString()||'';if(old&&!url.searchParams.get('offset'))throw Error('目标已存在');add(path,false,content);body={offset:content.length};writes.push({op,path,content})}
    else if(op==='transfer-status')body={exists:fs.has(path),size:fs.get(path)?.size||0}
    else if(op==='rename'||op==='copy'){if(fs.has(input.destination))throw Error('目标已存在');assert(fs.has(input.source));for(const item of [...fs.values()].filter(item=>item.path===input.source||item.path.startsWith(input.source+'/'))){const next=input.destination+item.path.slice(input.source.length);add(next,item.directory,item.content);if(op==='rename')fs.delete(item.path)}writes.push({op,...input})}
    else if(op==='delete'){for(const key of fs.keys())if(key===input.path||key.startsWith(input.path+'/'))fs.delete(key);writes.push({op,...input})}
    else if(op==='text')body={content:fs.get(path).content,version:'fixture',size:fs.get(path).size}
   }
  }catch(error){status=409;body={message:error.message}}
  await route.fulfill({status,json:body})
 })
 await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__files.html`)
 const dialog=page.getByRole('dialog',{name:'文件管理 · 开发服务器'}),list=dialog.getByRole('listbox',{name:'文件列表'}),row=name=>list.getByRole('option',{name,exact:true}),menu=page.getByRole('menu',{name:'文件管理操作'})
 await row('src').waitFor();await dialog.locator('.explorer-tree-entry[title="/project/src"]').waitFor()
 assert.equal(await row('.env').count(),0);assert.equal(await list.getByRole('option').count(),4)
 const folderRow=row('src'),fileRow=row('readme.md')
 assert(await folderRow.evaluate(element=>element.classList.contains('is-directory')));assert(await fileRow.evaluate(element=>element.classList.contains('is-file')))
 const folderStyle=await folderRow.evaluate(element=>({weight:getComputedStyle(element.querySelector('.explorer-name > span')).fontWeight,color:getComputedStyle(element.querySelector('svg')).color,fill:getComputedStyle(element.querySelector('svg')).fill}))
 const fileStyle=await fileRow.evaluate(element=>({weight:getComputedStyle(element.querySelector('.explorer-name > span')).fontWeight,color:getComputedStyle(element.querySelector('svg')).color,fill:getComputedStyle(element.querySelector('svg')).fill}))
 assert.notEqual(folderStyle.color,fileStyle.color);assert.notEqual(folderStyle.fill,fileStyle.fill);assert(Number(folderStyle.weight)>Number(fileStyle.weight))
 await row('readme.md').dblclick();const textEditor=page.locator('.text-editor-dialog');await textEditor.locator('.cm-editor').waitFor()
 const editorFrame=await textEditor.evaluate(element=>{const style=getComputedStyle(element),header=element.querySelector('.el-dialog__header').getBoundingClientRect(),body=getComputedStyle(element.querySelector('.el-dialog__body')),shell=getComputedStyle(element.querySelector('.text-editor-shell'));return{border:style.borderTopWidth,radius:style.borderRadius,headerHeight:header.height,bodyPadding:body.padding,shellBorder:shell.borderTopWidth,shellBackground:shell.backgroundColor}})
 assert.equal(editorFrame.border,'1px');assert.equal(editorFrame.radius,'6px');assert(editorFrame.headerHeight<=44);assert.equal(editorFrame.bodyPadding,'0px');assert.equal(editorFrame.shellBorder,'0px');assert.notEqual(editorFrame.shellBackground,'rgb(16, 27, 45)')
 await textEditor.screenshot({path:shots+'text-editor.png'});await textEditor.getByRole('button',{name:'关闭',exact:true}).click();await textEditor.waitFor({state:'hidden'})
 const left=await dialog.locator('.explorer-directories').boundingBox(),right=await list.boundingBox();assert(left.x+left.width<right.x&&left.height>350)
 const sep=dialog.getByRole('separator',{name:'调整文件夹栏宽度'}),box=await sep.boundingBox();await page.mouse.move(box.x+2,box.y+100);await page.mouse.down();await page.mouse.move(box.x+65,box.y+100,{steps:6});await page.mouse.up();assert(Number(await sep.getAttribute('aria-valuenow'))>=270)
 await dialog.screenshot({path:shots+'desktop.png'})
 await row('readme.md').click({button:'right'});await menu.getByRole('menuitem',{name:'属性',exact:true}).click();const properties=page.getByRole('dialog',{name:'属性',exact:true});await properties.getByText('/project/readme.md',{exact:true}).waitFor();await properties.getByRole('button',{name:'关闭',exact:true}).click()
 await row('readme.md').click({button:'right'});await page.screenshot({path:shots+'context-menu.png'});await menu.getByRole('menuitem',{name:'复制路径',exact:true}).click();assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),'/project/readme.md')
 await row('readme.md').click({button:'right'});await menu.getByRole('menuitem',{name:/重命名/}).click();await dialog.getByRole('textbox',{name:'文件新名称',exact:true}).fill('README.md');await dialog.getByRole('textbox',{name:'文件新名称',exact:true}).press('Enter');let prompt;await row('README.md').waitFor();assert(fs.has('/project/README.md'))
 const blank=async()=>{const b=await list.boundingBox();await list.click({position:{x:50,y:b.height-12},button:'right'})}
 await blank();await menu.getByRole('menuitem',{name:'新建文件',exact:true}).click();prompt=page.getByRole('dialog',{name:'新建文件',exact:true});await prompt.getByRole('textbox').fill('notes.txt');await prompt.getByRole('button',{name:'OK',exact:true}).click();await row('notes.txt').waitFor();assert(fs.has('/project/notes.txt'))
 await row('notes.txt').click({button:'right'});await menu.getByRole('menuitem',{name:/^复制\s*Ctrl/}).click();await blank();await menu.getByRole('menuitem',{name:/^粘贴/}).click();await row('notes - 副本.txt').waitFor();assert(fs.has('/project/notes - 副本.txt'))
 await row('notes.txt').click();await row('README.md').click({modifiers:['Control']});assert.equal(await list.locator('[aria-selected="true"]').count(),2)
 await page.keyboard.press('Control+x');await row('docs').dblclick();await row('guide.md').waitFor();await list.focus();await page.keyboard.press('Control+v');await row('notes.txt').waitFor();await row('README.md').waitFor();assert(!fs.has('/project/notes.txt'));assert(fs.has('/project/docs/notes.txt'))
 await dialog.getByRole('button',{name:'后退',exact:true}).click();await row('src').waitFor();await dialog.getByRole('button',{name:'前进',exact:true}).click();await row('notes.txt').waitFor();await dialog.getByRole('button',{name:'上一级',exact:true}).click();await row('src').waitFor()
 await row('config.json').dragTo(row('src'));await page.waitForFunction(()=>!document.querySelector('[aria-label="config.json"]'));assert(fs.has('/project/src/config.json'))
 // A folder cannot be moved inside itself; a selected row exposes safe destination operations.
 await row('src').click({button:'right'});await menu.getByRole('menuitem',{name:'移动到…',exact:true}).click();const picker=page.getByRole('dialog',{name:'移动到文件夹',exact:true});await picker.waitFor();await picker.getByRole('button',{name:'取消',exact:true}).click()
 await dialog.getByRole('textbox',{name:'搜索当前目录',exact:true}).fill('副本');assert.equal(await list.getByRole('option').count(),1);await dialog.getByRole('button',{name:'清除筛选',exact:true}).click()
 await dialog.getByRole('button',{name:'显示隐藏文件',exact:true}).click();await row('.env').waitFor()
 await row('notes - 副本.txt').click({button:'right'});await menu.getByRole('menuitem',{name:/^删除/}).click();const confirm=page.getByRole('dialog',{name:'删除远程文件',exact:true});await confirm.getByRole('button',{name:'删除',exact:true}).click();await row('notes - 副本.txt').waitFor({state:'detached'});assert(!fs.has('/project/notes - 副本.txt'))
 await dialog.locator('input[type="file"]').setInputFiles({name:'upload.txt',mimeType:'text/plain',buffer:Buffer.from('uploaded from browser')});await row('upload.txt').waitFor();assert.equal(fs.get('/project/upload.txt').content,'uploaded from browser')
 // Invalid names stay inline, refresh preserves filter/selection/scroll, and uploads never silently reuse an unrelated remote prefix.
 await row('upload.txt').click();await page.keyboard.press('F2');const renameInput=dialog.getByRole('textbox',{name:'文件新名称',exact:true});await renameInput.fill('../invalid');await renameInput.press('Enter');await page.waitForFunction(()=>document.querySelector('.inline-file-rename input')?.getAttribute('aria-invalid')==='true');assert(fs.has('/project/upload.txt'));await renameInput.press('Escape')
 await dialog.locator('input[type="file"]').setInputFiles({name:'upload.txt',mimeType:'text/plain',buffer:Buffer.from('replacement content')});const collision=page.getByRole('dialog',{name:'同名项目',exact:true});await collision.getByRole('button',{name:'使用新名称',exact:true}).click();await row('upload - 副本.txt').waitFor();assert.equal(fs.get('/project/upload.txt').content,'uploaded from browser');assert.equal(fs.get('/project/upload - 副本.txt').content,'replacement content')
 for(let i=0;i<70;i++)add('/project/z-check-'+i+'.txt',false,'fixture');await dialog.getByRole('button',{name:'刷新',exact:true}).click();await row('z-check-50.txt').waitFor();await dialog.getByRole('textbox',{name:'搜索当前目录',exact:true}).fill('z-check');await row('z-check-50.txt').click();await list.evaluate(el=>el.scrollTop=300);const savedScroll=await list.evaluate(el=>el.scrollTop);await dialog.getByRole('button',{name:'刷新',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.explorer-files')?.getAttribute('aria-busy')==='false');assert.equal(await dialog.getByRole('textbox',{name:'搜索当前目录',exact:true}).inputValue(),'z-check');assert.equal(await row('z-check-50.txt').getAttribute('aria-selected'),'true');assert(Math.abs(await list.evaluate(el=>el.scrollTop)-savedScroll)<2);await dialog.getByRole('button',{name:'清除筛选',exact:true}).click()
 // Current-folder path input navigates without changing the tree's meaning; failures retain the old list.
 await dialog.getByRole('textbox',{name:'目录路径',exact:true}).fill('/missing');await page.keyboard.press('Enter');await dialog.getByRole('alert').waitFor();await row('src').waitFor();await dialog.getByRole('button',{name:'刷新',exact:true}).click();await dialog.getByRole('alert').waitFor({state:'hidden'})
 await page.setViewportSize({width:390,height:844});await dialog.getByRole('button',{name:'切换目录栏',exact:true}).click();await dialog.locator('.explorer-directories:visible').waitFor();await dialog.locator('.explorer-tree-entry[title="/project/docs"] > span').click();await row('README.md').waitFor();assert.equal(await dialog.locator('.explorer-directories:visible').count(),0)
 assert.equal(await dialog.evaluate(el=>el.scrollWidth>el.clientWidth),false);assert.equal(await dialog.locator('.explorer-toolbar').evaluate(el=>el.scrollWidth>el.clientWidth),false);await page.screenshot({path:shots+'mobile.png'})
 assert.deepEqual(errors,[]);assert(writes.some(item=>item.op==='rename')&&writes.some(item=>item.op==='copy')&&writes.some(item=>item.op==='delete')&&writes.some(item=>item.op==='upload'))
 console.log('PASS: directory tree and resizable dual panes, file/blank/tree menus, properties/copy path, create/rename, collision-safe copy, multiselect cut/paste, history, drag move, destination picker, filtering/hidden files, delete confirmation, real multipart upload, retained list on errors and mobile navigation')
}catch(error){const page=browser?.contexts()[0]?.pages()[0];if(page){await page.screenshot({path:shots+'failure.png'});console.error(await page.locator('body').innerText())}throw error}finally{await browser?.close();await server.close()}
