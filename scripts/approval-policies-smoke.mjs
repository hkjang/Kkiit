#!/usr/bin/env node
// Real API + embedded SPA regression test. Requires Docker, Go, Node 22+ and
// google-chrome (or KKIIT_SMOKE_CHROME). Run npm --prefix web run build first.
// Owns a disposable PostgreSQL and local server; never accepts a deployment URL.
// Uses random loopback ports. No credentials are written to disk or printed.
// Optional KKIIT_APPROVAL_SMOKE_OUTPUT stores screenshots and non-secret evidence.
import assert from 'node:assert/strict'
import { spawn, execFileSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
let base
const endpoint = '/api/v1/admin/approvals/policies'
const output = process.env.KKIIT_APPROVAL_SMOKE_OUTPUT
if (output) mkdirSync(output, { recursive: true })
const temp = mkdtempSync(path.join(output || tmpdir(), 'approval-smoke-'))
const container = `kkiit-approval-${randomBytes(6).toString('hex')}`
const password = randomBytes(24).toString('hex')
const cookies = new Map()
const evidence = []
let server, chrome, socket
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
async function until(check, label) {
  for (let i = 0; i < 200; i++) {
    if (await check()) return
    await delay(100)
  }
  throw new Error(`Timed out: ${label}`)
}
async function api(method, url, body) {
  const response = await fetch(base + url, {
    method, headers: { Cookie: [...cookies].map(([k, v]) => `${k}=${v}`).join('; '), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  for (const raw of response.headers.getSetCookie()) {
    const pair = raw.split(';')[0], at = pair.indexOf('=')
    cookies.set(pair.slice(0, at), pair.slice(at + 1))
  }
  assert.ok(response.ok, `${method} ${url}: ${response.status}`)
  return response.status === 204 ? undefined : response.json()
}
const cases = [
  { name: '제한 없음', conditions: {}, text: ['조건 없음 · 모든 상품에 적용'] },
  { name: '최소 금액 0', conditions: { min_amount: 0 }, text: ['최소 금액 0원 이상'] },
  { name: '금액 경계', conditions: { min_amount: 123456, max_amount: 9876543 }, text: ['최소 금액 123,456원 이상', '최대 금액 9,876,543원 이하'] },
  { name: '복수 유형과 등급', conditions: { service_types: ['HUMAN', 'AI', 'HYBRID'], seller_levels: ['NEW', 'RISING', 'PRO', 'ELITE'] }, text: ['서비스 유형 HUMAN, AI, HYBRID', '판매자 등급 NEW, RISING, PRO, ELITE'] },
  { name: '품질 0', conditions: { quality_score_below: 0 }, text: ['품질 점수 0 미만', '미산정 상품 포함'] },
  { name: '품질 임계값', conditions: { quality_score_below: 75.5 }, text: ['품질 점수 75.5 미만', '미산정 상품 포함'] },
  { name: '빈 배열', conditions: { service_types: [], seller_levels: [] }, text: ['조건 없음 · 모든 상품에 적용'] },
  { name: '최대 금액 0', conditions: { max_amount: 0 }, text: ['최대 금액 0원 이하'] },
  { name: '알 수 없는 키', conditions: { future_condition: true }, text: ['일부 조건 확인 필요'], invalid: true },
  { name: '잘못된 배열', conditions: { service_types: 'AI', seller_levels: [1, null] }, text: ['일부 조건 확인 필요'], invalid: true },
  { name: '정상 조건과 잘못된 조건', conditions: { min_amount: 0, service_types: ['AI', 1] }, text: ['최소 금액 0원 이상', '일부 조건 확인 필요'], invalid: true },
]

try {
  // Run both processes in our container so the application's fixed port 8080
  // cannot touch an existing local service. The Go binary is static for Alpine.
  execFileSync('go', ['build', '-o', path.join(temp, 'kkiit'), './cmd/kkiit'], { cwd: root, env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'inherit' })
  execFileSync('docker', ['run', '--rm', '-d', '--name', container, '-e', 'POSTGRES_HOST_AUTH_METHOD=trust', '-p', '127.0.0.1::8080', 'postgres:16-alpine'], { stdio: 'ignore' })
  await until(async () => { try { execFileSync('docker', ['exec', container, 'pg_isready', '-U', 'postgres'], { stdio: 'ignore' }); return true } catch { return false } }, 'PostgreSQL')
  const port = execFileSync('docker', ['port', container, '8080/tcp'], { encoding: 'utf8' }).trim().split(':').at(-1)
  base = `http://127.0.0.1:${port}`
  execFileSync('docker', ['cp', path.join(temp, 'kkiit'), `${container}:/tmp/kkiit`], { stdio: 'ignore' })
  server = spawn('docker', ['exec', ...['POSTGRES_DSN', 'BOOTSTRAP_ADMIN', 'BOOTSTRAP_ADMIN_PASSWORD', 'ENCRYPTION_KEY', 'SHUTDOWN_DRAIN_SECONDS'].flatMap((key) => ['-e', key]), container, '/tmp/kkiit'], { stdio: 'ignore', env: { ...process.env,
    POSTGRES_DSN: 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable',
    BOOTSTRAP_ADMIN: 'approval-smoke', BOOTSTRAP_ADMIN_PASSWORD: password,
    ENCRYPTION_KEY: randomBytes(32).toString('hex'), SHUTDOWN_DRAIN_SECONDS: '0',
  } })
  await until(async () => { try { return (await fetch(base + '/health')).ok } catch { return false } }, 'Go server')
  await api('POST', '/api/v1/auth/login', { username: 'approval-smoke', password })
  for (const item of cases) {
    const saved = await api('POST', endpoint, { name: item.name, resource_type: 'talent_publish', enabled: false, priority: 100, conditions: item.conditions, steps: [{ role: 'operator', min_approvals: 1 }] })
    item.id = saved.id
  }
  const stored = (await api('GET', endpoint)).items
  for (const item of cases) {
    const actual = stored.find((p) => p.id === item.id).conditions
    assert.deepEqual(actual, item.conditions)
    evidence.push({ stage: 'POST -> GET', name: item.name, conditions: actual })
  }

  chrome = spawn(process.env.KKIIT_SMOKE_CHROME || 'google-chrome', ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--lang=ko-KR', `--user-data-dir=${temp}`, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
  await until(async () => existsSync(path.join(temp, 'DevToolsActivePort')), 'Chrome')
  const chromePort = readFileSync(path.join(temp, 'DevToolsActivePort'), 'utf8').split('\n')[0]
  const targets = await fetch(`http://127.0.0.1:${chromePort}/json/list`).then((r) => r.json())
  socket = new WebSocket(targets.find((t) => t.type === 'page').webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }) })
  let id = 0
  const pending = new Map(), errors = []
  socket.addEventListener('message', ({ data }) => {
    const message = JSON.parse(data)
    if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails.text)
    if (message.method === 'Page.javascriptDialogOpening') void send('Page.handleJavaScriptDialog', { accept: true, promptText: '스모크 검토' })
    if (!pending.has(message.id)) return
    const { resolve, reject } = pending.get(message.id)
    pending.delete(message.id)
    if (message.error) reject(new Error(message.error.message))
    else resolve(message.result)
  })
  function send(method, params = {}) {
    return new Promise((resolve, reject) => {
      pending.set(++id, { resolve, reject })
      socket.send(JSON.stringify({ id, method, params }))
    })
  }
  async function evaluate(expression) {
    const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
    assert.ok(!result.exceptionDetails, result.exceptionDetails?.text)
    return result.result.value
  }
  const button = async (text) => assert.ok(await evaluate(`(() => { const b = [...document.querySelectorAll('button')].find(b => b.textContent.trim() === ${JSON.stringify(text)}); b?.click(); return !!b })()`), `button ${text}`)
  const cardExpression = (item) => `document.querySelector('button[aria-label=${JSON.stringify(item.name + ' 편집')}]')?.closest('.MuiCard-root')`
  const cardText = (item) => evaluate(`${cardExpression(item)}?.innerText`)
  async function checkCard(item) {
    await until(async () => Boolean(await cardText(item)), item.name)
    const text = await cardText(item)
    for (const expected of item.text) assert.ok(text.includes(expected), `${item.name}: missing ${expected}; rendered ${text}`)
    if (item.invalid) assert.ok(!text.includes('모든 상품에 적용'), item.name)
    evidence.push({ stage: 'rendered card', name: item.name, conditions: item.conditions, card: text })
  }
  async function navigate() {
    await evaluate('window.__approvalSmokeNavigating = true')
    await send('Page.navigate', { url: base + '/admin/approvals' })
    await until(async () => {
      try { return await evaluate(`!window.__approvalSmokeNavigating && document.readyState === 'complete' && !!(${cardExpression(cases[0])})`) } catch { return false }
    }, 'new document with approval cards')
  }
  async function screenshot(name) {
    if (!output) return
    await evaluate(`Promise.all(document.getAnimations().map(a => a.finished.catch(() => {})))`)
    const { data } = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false })
    writeFileSync(path.join(output, name + '.png'), Buffer.from(data, 'base64'))
  }
  await send('Page.enable'); await send('Runtime.enable'); await send('Network.enable')
  for (const [name, value] of cookies) await send('Network.setCookie', { name, value, url: base, path: '/' })
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false })
  await navigate()
  for (const item of cases) await checkCard(item)
  await screenshot('approval-desktop')

  // Every valid API policy traverses GET -> rendered card -> actual edit form
  // -> UI save -> GET -> reloaded card. Empty arrays intentionally normalize to {}.
  for (const item of cases.filter((c) => !c.invalid)) {
    await evaluate(`${cardExpression(item)}.querySelector('button').click()`)
    await until(async () => evaluate(`!!document.querySelector('[role="dialog"] input')`), 'edit dialog')
    const values = await evaluate(String.raw`Object.fromEntries([...document.querySelectorAll('[role="dialog"] .MuiTextField-root')].map(e => [e.querySelector('label').textContent.replace(/\s*\*$/, ''), e.querySelector('input').value]))`)
    const c = item.conditions
    assert.equal(values['최소 금액'], String(c.min_amount ?? ''))
    assert.equal(values['최대 금액'], String(c.max_amount ?? ''))
    assert.equal(values['품질 점수가 이 값 미만'], String(c.quality_score_below ?? ''))
    assert.equal(values['서비스 유형'], (c.service_types ?? []).join(', '))
    assert.equal(values['판매자 등급'], (c.seller_levels ?? []).join(', '))
    evidence.push({ name: item.name, form: values })
    if (c.min_amount === 123456) await screenshot('approval-edit')
    await button('저장')
    await until(async () => evaluate(`!document.querySelector('[role="dialog"]')`), 'saved edit')
    const expected = Object.fromEntries(Object.entries(c).filter(([, v]) => !Array.isArray(v) || v.length))
    const reloaded = (await api('GET', endpoint)).items.find((p) => p.id === item.id).conditions
    assert.deepEqual(reloaded, expected)
    evidence.push({ stage: 'edit -> save -> GET', name: item.name, conditions: reloaded })
    await navigate(); await checkCard(item)
  }
  // Responsive layout: summary and controls must not overlap or overflow.
  await send('Emulation.setDeviceMetricsOverride', { width: 375, height: 900, deviceScaleFactor: 1, mobile: true })
  await delay(250)
  for (const item of cases) {
    const layout = await evaluate(`(() => { const c = ${cardExpression(item)}; const text = c.querySelector('h4').parentElement; const controls = c.querySelector('button').parentElement; const a = text.getBoundingClientRect(), b = controls.getBoundingClientRect(); return { overlap: a.bottom > b.top, right: b.right, scroll: c.scrollWidth, width: c.clientWidth } })()`)
    assert.equal(layout.overlap, false, item.name)
    assert.ok(layout.right <= 375 && layout.scroll <= layout.width + 1, JSON.stringify(layout))
  }
  await evaluate(`${cardExpression(cases[3])}.scrollIntoView({ block: 'center' })`)
  await screenshot('approval-mobile')
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false })
  // Existing enable/disable and delete controls still reach the real API.
  const item = cases[3]
  await evaluate(`${cardExpression(item)}.querySelector('input[type="checkbox"]').click()`)
  await until(async () => (await api('GET', endpoint)).items.find((p) => p.id === item.id).enabled, 'toggle on')
  assert.deepEqual((await api('GET', endpoint)).items.find((p) => p.id === item.id).conditions, item.conditions)
  await until(async () => evaluate(`${cardExpression(item)}.querySelector('input').checked`), 'toggle rendered')
  await evaluate(`${cardExpression(item)}.querySelector('input[type="checkbox"]').click()`)
  await until(async () => !(await api('GET', endpoint)).items.find((p) => p.id === item.id).enabled, 'toggle off')
  await evaluate(`${cardExpression(item)}.querySelectorAll('button')[1].click()`)
  await until(async () => !(await api('GET', endpoint)).items.some((p) => p.id === item.id), 'delete')
  // Create and change a policy through actual controlled React form inputs.
  async function fill(label, value) {
    await evaluate(`(() => {
      const el = [...document.querySelectorAll('[role="dialog"] .MuiTextField-root')].find(e => e.querySelector('label').textContent.startsWith(${JSON.stringify(label)})).querySelector('input')
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(el, ${JSON.stringify(value)})
      el.dispatchEvent(new Event('input', { bubbles: true }))
    })()`)
  }
  await button('정책 추가')
  await until(async () => evaluate(`!!document.querySelector('[role="dialog"] input')`), 'create dialog')
  for (const [label, value] of Object.entries({ '정책 이름': '화면 생성과 편집', '최소 금액': '0', '최대 금액': '2000000', '서비스 유형': 'HUMAN, AI', '판매자 등급': 'NEW, PRO', '품질 점수가 이 값 미만': '75.5' })) await fill(label, value)
  await button('저장')
  await until(async () => evaluate(`!document.querySelector('[role="dialog"]')`), 'created policy')
  const created = (await api('GET', endpoint)).items.find((p) => p.name === '화면 생성과 편집')
  assert.deepEqual(created.conditions, { min_amount: 0, max_amount: 2000000, service_types: ['HUMAN', 'AI'], seller_levels: ['NEW', 'PRO'], quality_score_below: 75.5 })
  const uiCase = { ...created, text: ['최소 금액 0원 이상', '최대 금액 2,000,000원 이하', '품질 점수 75.5 미만', '서비스 유형 HUMAN, AI', '판매자 등급 NEW, PRO'] }
  await navigate(); await checkCard(uiCase)
  await evaluate(`${cardExpression(uiCase)}.querySelector('button').click()`)
  await until(async () => evaluate(`!!document.querySelector('[role="dialog"] input')`), 'edit UI-created policy')
  await fill('최소 금액', '1000'); await fill('품질 점수가 이 값 미만', '0')
  await button('저장')
  await until(async () => evaluate(`!document.querySelector('[role="dialog"]')`), 'save changed values')
  uiCase.conditions = { ...created.conditions, min_amount: 1000, quality_score_below: 0 }
  uiCase.text = ['최소 금액 1,000원 이상', '최대 금액 2,000,000원 이하', '품질 점수 0 미만', '미산정 상품 포함']
  assert.deepEqual((await api('GET', endpoint)).items.find((p) => p.id === created.id).conditions, uiCase.conditions)
  await navigate(); await checkCard(uiCase)
  await evaluate(`${cardExpression(uiCase)}.scrollIntoView()`)
  await screenshot('approval-created-edited')

  // A real publish with no quality score must still enter review at threshold 0.
  // All other policies remain disabled. The seller is this disposable admin.
  await api('PUT', '/api/v1/me/seller-profile', { seller_type: 'individual', headline: '승인 화면 검증', biography: '버릴 테스트 계정', skills: [], capacity: 5, settings: {} })
  for (const decision of ['approved', 'rejected']) {
    const talent = await api('POST', '/api/v1/talents', { title: `품질 미산정 검토 ${decision}`, summary: '승인 조건 화면 검증', description: '실제 상품 공개와 승인 요청 검토 테스트', service_type: 'HUMAN', base_price: 1000, delivery_days: 1 })
    const detail = await api('GET', `/api/v1/talents/${talent.id}`)
    assert.equal(detail.quality_score, null)
    const published = await api('POST', `/api/v1/talents/${talent.id}/publish`)
    assert.equal(published.approval_required, true, 'unscored product matches quality below 0')
    const request = (await api('GET', '/api/v1/admin/approvals/requests')).items.find((r) => r.resource_id === talent.id)
    assert.equal(request.policy_name, created.name)
    await navigate()
    await button('내용 보기')
    await until(async () => evaluate(`document.querySelector('[role="dialog"]')?.innerText.includes(${JSON.stringify('품질 미산정 검토 ' + decision)})`), 'real review dialog')
    await screenshot(`approval-review-${decision}`)
    await evaluate(`(() => { const b = [...document.querySelectorAll('[role="dialog"] button')].find(b => b.textContent.trim() === ${JSON.stringify(decision === 'approved' ? '승인' : '반려')}); b.click() })()`)
    await until(async () => (await api('GET', `/api/v1/admin/approvals/requests/${request.id}`)).state === decision, 'review decision persisted')
    evidence.push({ request: decision, quality_score: detail.quality_score, matched_policy: created.id })
  }
  assert.deepEqual(errors, [], 'browser runtime errors')
  evidence.push({ controls: 'UI create/change, toggle on/off preserving array conditions, delete, approve/reject passed', viewport: '375px: no overlap or card overflow', browserErrors: errors })
  console.log(`PASS: ${cases.length} real API/card cases, ${cases.filter((c) => !c.invalid).length} edit/save round trips, UI create/change, mobile layout, toggle/delete, unscored publish and approve/reject`)
} finally {
  if (output) writeFileSync(path.join(output, 'approval-evidence.json'), JSON.stringify(evidence, null, 2) + '\n')
  socket?.close()
  chrome?.kill()
  server?.kill()
  try { execFileSync('docker', ['rm', '-f', container], { stdio: 'ignore' }) } catch { /* startup failed */ }
  await delay(500)
  rmSync(temp, { recursive: true, force: true })
}
