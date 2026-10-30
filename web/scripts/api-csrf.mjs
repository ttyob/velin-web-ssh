import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('../src/api.ts', import.meta.url), 'utf8')
const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ES2022, target: ts.ScriptTarget.ES2022 } }).outputText
let instance = 0
const loadAPI = () => import(`data:text/javascript;base64,${Buffer.from(output).toString('base64')}#csrf-test-${instance++}`)
const jsonResponse = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const deferred = () => {
  let resolve
  const promise = new Promise(done => { resolve = done })
  return { promise, resolve }
}
const originalFetch = globalThis.fetch

try {
  // Initial requests share one bootstrap, including its response cookie.
  {
    const { csrfHeaders } = await loadAPI()
    const bootstrap = deferred()
    let calls = 0
    globalThis.fetch = async (path, init) => {
      assert.equal(path, '/api/csrf')
      assert.equal(init.credentials, 'same-origin')
      assert.equal(init.cache, 'no-store')
      calls++
      return bootstrap.promise
    }
    const requests = Array.from({ length: 12 }, () => csrfHeaders())
    assert.equal(calls, 1)
    bootstrap.resolve(jsonResponse({ token: 'initial-token' }))
    for (const headers of await Promise.all(requests)) assert.equal(headers['X-CSRF-Token'], 'initial-token')
    await csrfHeaders()
    assert.equal(calls, 1)
  }

  // Concurrent 403s share one refresh. A late 403 from the old token must
  // reuse the new token instead of invalidating it and bootstrapping again.
  {
    const { api, csrfHeaders } = await loadAPI()
    const refreshStarted = deferred()
    const refresh = deferred()
    const lateRejection = deferred()
    let bootstraps = 0
    let oldRequests = 0
    let retries = 0
    globalThis.fetch = async (path, init) => {
      if (path === '/api/csrf') {
        bootstraps++
        if (bootstraps === 1) return jsonResponse({ token: 'old-token' })
        refreshStarted.resolve()
        return refresh.promise
      }
      assert.equal(init.credentials, 'same-origin')
      const token = new Headers(init.headers).get('X-CSRF-Token')
      if (token === 'old-token') {
        oldRequests++
        if (oldRequests === 3) return lateRejection.promise
        return jsonResponse({ code: 'csrf_rejected' }, 403)
      }
      assert.equal(token, 'new-token')
      retries++
      return jsonResponse({ ok: true })
    }
    await csrfHeaders()
    const requests = Array.from({ length: 3 }, () => api('/api/example', { method: 'POST', body: '{}' }))
    await refreshStarted.promise
    await new Promise(resolve => setImmediate(resolve))
    assert.equal(bootstraps, 2)
    refresh.resolve(jsonResponse({ token: 'new-token' }))
    await Promise.all(requests.slice(0, 2))
    lateRejection.resolve(jsonResponse({ code: 'csrf_rejected' }, 403))
    assert.deepEqual(await requests[2], { ok: true })
    assert.equal(bootstraps, 2)
    assert.equal(oldRequests, 3)
    assert.equal(retries, 3)
  }

  // Failed bootstrap can recover, and unrelated 403 responses are not retried.
  {
    const { api, csrfHeaders } = await loadAPI()
    let bootstraps = 0
    let writes = 0
    globalThis.fetch = async path => {
      if (path === '/api/csrf') return jsonResponse(++bootstraps === 1 ? {} : { token: 'valid-token' })
      writes++
      return jsonResponse({ code: 'cross_site_rejected', message: 'blocked' }, 403)
    }
    await assert.rejects(csrfHeaders(), /无法建立安全会话/)
    assert.equal((await csrfHeaders())['X-CSRF-Token'], 'valid-token')
    await assert.rejects(api('/api/example', { method: 'POST' }), error => error.body.code === 'cross_site_rejected')
    assert.equal(bootstraps, 2)
    assert.equal(writes, 1)
  }

  // Even CSRF failures are retried at most once.
  {
    const { api } = await loadAPI()
    let bootstraps = 0
    let writes = 0
    globalThis.fetch = async path => {
      if (path === '/api/csrf') return jsonResponse({ token: `token-${++bootstraps}` })
      writes++
      return jsonResponse({ code: 'csrf_rejected', message: 'blocked' }, 403)
    }
    await assert.rejects(api('/api/example', { method: 'POST' }), error => error.body.code === 'csrf_rejected')
    assert.equal(bootstraps, 2)
    assert.equal(writes, 2)
  }
} finally {
  globalThis.fetch = originalFetch
}

console.log('API CSRF recovery tests passed')
