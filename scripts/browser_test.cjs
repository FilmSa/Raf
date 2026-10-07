// Browser acceptance test. Requires Playwright and an installed Edge browser.
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const net = require('node:net');
const {spawn, spawnSync} = require('node:child_process');
const assert = require('node:assert/strict');
const {chromium} = require('playwright');
const root = path.resolve(__dirname,'..');
const processes = [];
let browser;
let testDirectory;
function port() { return new Promise((resolve,reject) => { const socket = net.createServer(); socket.once('error',reject); socket.listen(0,'127.0.0.1',() => { const value = socket.address().port; socket.close(() => resolve(value)); }); }); }
async function ready(base,child) {
  for (let i=0;i<100;i++) {
    if (child.exitCode !== null) throw new Error(`Service exited: ${child.exitCode}`);
    try { const response = await fetch(base+'/health'); if(response.ok) return; } catch {}
    await new Promise(resolve => setTimeout(resolve,200));
  }
  throw new Error('Service did not become ready');
}
async function run() {
  fs.mkdirSync(path.join(root,'.runtime'),{recursive:true});
  testDirectory = fs.mkdtempSync(path.join(root,'.runtime','browser-'));
  const facePort = await port(); const serverPort = await port();
  const base = `http://127.0.0.1:${serverPort}`;
  const env = {...process.env,APP_ADDR:`127.0.0.1:${serverPort}`,ADMIN_PASSWORD:'browser-test-password',
    DB_PATH:path.join(testDirectory,'test.db'),FACE_SERVICE_URL:`http://127.0.0.1:${facePort}`,DEVICE_API_KEY:'browser-device-key',COOKIE_SECURE:'false',FACE_THRESHOLD:'0.363'};
  const python = path.join(root,'.venv',process.platform === 'win32' ? 'Scripts/python.exe' : 'bin/python');
  const face = spawn(python,['-m','uvicorn','app:app','--app-dir',path.join(root,'face-service'),'--host','127.0.0.1','--port',String(facePort),'--no-access-log'],{cwd:root,env,stdio:'ignore',windowsHide:true});
  processes.push(face); await ready(env.FACE_SERVICE_URL,face);
  const server = spawn(path.join(root,'bin',process.platform==='win32'?'server.exe':'server'),[],{cwd:root,env,stdio:'ignore',windowsHide:true});
  processes.push(server); await ready(base,server);
  browser = await chromium.launch({channel:process.env.PLAYWRIGHT_CHANNEL || 'msedge',headless:true});
  const page = await browser.newPage({viewport:{width:1440,height:1000},deviceScaleFactor:1});
  const errors = [];
  page.on('pageerror',error=>errors.push(error.message));
  page.on('console',message=>{if(message.type()==='error' && /Content Security Policy|Refused/.test(message.text()))errors.push(message.text());});
  await page.goto(base);
  await page.locator('#password').fill('browser-test-password');
  await page.locator('#login-form button').click();
  await page.locator('#app-view').waitFor({state:'visible'});
  await page.getByText('Система готова к работе',{exact:true}).waitFor();
  await page.screenshot({path:path.join(root,'.runtime','dashboard-empty.png'),fullPage:true});
  await page.locator('#page-journal [data-open-user]').click();
  await page.locator('#full-name').fill('Тестовый сотрудник');
  await page.locator('#card-uid').fill('04a1b2c3d4e5f6');
  assert.equal(await page.locator('#card-uid').inputValue(),'04A1B2C3D4E5F6');
  await page.locator('#user-photo').setInputFiles(path.join(root,'.runtime','fixtures','lena.jpg'));
  await page.locator('#save-user').click();
  await page.locator('#user-dialog').waitFor({state:'hidden'});
  await page.locator('[data-page="users"]').click();
  await page.locator('#users-body').getByText('Тестовый сотрудник',{exact:true}).waitFor();
  await page.screenshot({path:path.join(root,'.runtime','users.png'),fullPage:true});
  await page.locator('[data-page="terminal"]').click();
  await page.locator('#access-uid').fill('04A1B2C3D4E5F6');
  await page.locator('#access-photo').setInputFiles(path.join(root,'.runtime','fixtures','lena.jpg'));
  await page.locator('#access-form button').click();
  await page.locator('#access-result strong').getByText('Доступ разрешён',{exact:true}).waitFor();
  await page.locator('[data-page="journal"]').click();
  await page.locator('#logs-body').getByText('Доступ разрешён',{exact:true}).waitFor();
  await page.locator('#log-search').fill('НЕСУЩЕСТВУЮЩИЙ');
  await page.locator('#logs-body').getByText('Событий пока нет',{exact:true}).waitFor();
  await page.locator('#reset-filters').click();
  await page.locator('#logs-body').getByText('Тестовый сотрудник',{exact:true}).waitFor();
  await page.screenshot({path:path.join(root,'.runtime','dashboard.png'),fullPage:true});
  await page.setViewportSize({width:390,height:844});
  await page.waitForFunction(()=>document.querySelector('#sidebar').getBoundingClientRect().right<=0);
  assert(await page.evaluate(()=>document.documentElement.scrollWidth <= window.innerWidth),'Page overflows mobile viewport');
  await page.screenshot({path:path.join(root,'.runtime','dashboard-mobile.png'),fullPage:true});
  await page.locator('#menu-toggle').click();
  await page.locator('[data-page="users"]').click();
  await page.locator('[data-toggle-user]').click();
  await page.locator('#users-body').getByText('Заблокирован',{exact:true}).waitFor();
  await page.locator('[data-toggle-user]').click();
  await page.locator('#users-body').getByText('Активен',{exact:true}).waitFor();
  await page.locator('[data-delete-user]').click();
  await page.locator('#confirm-delete').click();
  await page.locator('#users-body').getByText('Добавьте первого сотрудника',{exact:true}).waitFor();
  await page.setViewportSize({width:1440,height:1000});
  await page.locator('#logout').click();
  await page.locator('#login-view').waitFor({state:'visible'});
  assert.deepEqual(errors,[],`Browser errors: ${errors.join('; ')}`);
}
async function cleanup() {
  if(browser)await browser.close();
  for(const child of processes.reverse()) {
    if(child.exitCode!==null)continue;
    let timeout;
    const exited = new Promise((resolve,reject)=>{
      child.once('exit',resolve);
      timeout=setTimeout(()=>reject(new Error(`Child cleanup timed out: ${child.pid}`)),10000);
    });
    if(process.platform==='win32') {
      const stopped=spawnSync('taskkill',['/PID',String(child.pid),'/T','/F'],{stdio:'ignore',windowsHide:true});
      if(stopped.error)throw stopped.error;
    } else child.kill('SIGTERM');
    try { await exited; } finally { clearTimeout(timeout); }
  }
  if(testDirectory) {
    const resolved = path.resolve(testDirectory);
    assert(resolved.startsWith(path.join(root,'.runtime')+path.sep),'Unsafe cleanup path');
    if(process.platform==='win32') {
      // Use native PowerShell deletion: some Windows Node versions leave
      // rmSync directories behind even after the child handles are closed.
      const literal=resolved.replace(/'/g,"''");
      const removed=spawnSync('powershell.exe',['-NoProfile','-Command',`$ErrorActionPreference='Stop'; Remove-Item -LiteralPath '${literal}' -Recurse -Force`],{encoding:'utf8',windowsHide:true});
      if(removed.error || removed.status!==0)throw new Error(removed.stderr || String(removed.error));
    } else fs.rmSync(resolved,{recursive:true,force:true});
    assert(!fs.existsSync(resolved),'Temporary database was not removed');
    testDirectory=null;
  }
}
run().then(async()=>{await cleanup();console.log('PASS browser: login, registration, UID normalization, access, log search, mobile layout, block/unblock, deletion, logout, CSP, process cleanup');}).catch(async(error)=>{console.error(error);try{await cleanup();}catch(cleanupError){console.error(cleanupError);}process.exitCode=1;});
