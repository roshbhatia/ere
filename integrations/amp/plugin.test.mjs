import { test } from 'node:test'
import assert from 'node:assert/strict'
import plugin from './lifier/index.ts'
import { settings } from './lifier/settings.ts'
import { threadSnapshot } from './lifier/thread.ts'

function harness({ timeout = false, remote = false } = {}) {
  const tools = new Map(), calls = [], registeredSkills = []
  const allocation = { id: 'allocation-1', runner: 'test', runnerId: 'amp-test', provider: 'lima', state: 'allocated' }

  const amp = {
    system: { executor: { kind: remote ? 'remote' : 'local' } },
    $: async (template, ...values) => {
      if (template.join('').includes('--executor')) { calls.push(['submit-thread', { executor: values[0], mode: values[1], label: values[3], prompt: values[4] }]); return { exitCode: 0, stdout: 'https://ampcode.com/threads/T-1234abcd', stderr: '' } }
      if (template.join('').includes('threads export')) return { exitCode: 0, stdout: JSON.stringify({ meta: { lastKnownAgentState: { state: timeout ? 'running' : 'idle', messageID: 'M-final' } }, messages: [{ role: 'assistant', protocolMessageID: 'M-final', state: { type: 'complete', stopReason: 'end_turn' }, content: 'done' }] }), stderr: '' }
      const operation = values[1]
      const input = JSON.parse(values[2])
      calls.push([operation, input])
      let output = {}
      if (operation === 'runner_acquire') output = allocation
      if (operation === 'runner_activity') Object.assign(allocation, input)
      if (operation === 'runner_allocations') output = { allocations: [allocation] }
      return { exitCode: 0, stdout: JSON.stringify(output), stderr: '' }
    },
    registerTool: tool => tools.set(tool.name, tool),
    registerCommand() {},
    registerSkill: async skill => registeredSkills.push(skill),
  }
  return { amp, tools, calls, registeredSkills }
}

test('remote submission persists unknown allocation first and preserves mode', async () => {
  const h = harness(); await plugin(h.amp)
  const result = JSON.parse(await h.tools.get('runner_run').execute({ runner: 'test', prompt: 'do work', mode: 'medium' }))
  assert.equal(result.threadId, 'T-1234abcd')
  const submitted = h.calls.find(call => Array.isArray(call) && call[0] === 'submit-thread')
  assert.deepEqual(submitted[1], { mode: 'medium', executor: 'runner:amp-test', label: 'lifier-allocation-allocation-1', prompt: 'do work' })
  const unknown = h.calls.findIndex(call => Array.isArray(call) && call[0] === 'runner_activity' && call[1].state === 'unknown')
  assert.ok(unknown < h.calls.indexOf(submitted))
  assert.equal(h.registeredSkills[0].path, 'skills/runner-workflow')
  assert.equal(h.calls.filter(call => Array.isArray(call) && call[0] === 'runner_release').length, 0)
})

test('timeout retains unknown activity', async () => {
  settings.waitTimeoutMs = 10; settings.pollIntervalMs = 1
  const h = harness({ timeout: true }); await plugin(h.amp)
  const result = JSON.parse(await h.tools.get('runner_run').execute({ runner: 'test', prompt: 'do work', mode: 'high' }))
  assert.equal(result.state, 'unknown'); assert.equal(result.retained, true)
  assert.equal(h.calls.at(-1)[1].state, 'unknown')
})

test('remote workers do not register operator tools', async () => {
  const h = harness({ remote: true }); await plugin(h.amp)
  assert.equal(h.tools.size, 0)
})

test('worker role does not register operator tools', async () => {
  settings.role = 'worker'
  try { const h = harness(); await plugin(h.amp); assert.equal(h.tools.size, 0) }
  finally { settings.role = 'operator' }
})

test('stale idle state cannot release a newly queued turn', () => {
  const snapshot = threadSnapshot({ meta: { lastKnownAgentState: { state: 'idle', messageID: 'M-old' } }, messages: [{ role: 'user', content: 'next turn' }] })
  assert.equal(snapshot.state, 'unknown')
})
