import type { PluginAPI } from '@ampcode/plugin'
import { settings } from './settings.ts'
import { threadSnapshot } from './thread.ts'

export const description = 'Run and track Amp tasks on configured Docker, Lima, Kubernetes Pod, and KubeVirt runners.'

type Allocation = { id: string; runner: string; runnerId: string; provider: string; threadId?: string; state: string }

function object(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('Expected an object')
  return Object.fromEntries(Object.entries(value))
}
function text(input: Record<string, unknown>, key: string): string {
  const value = input[key]
  if (typeof value !== 'string' || value.trim() === '') throw new Error(`${key} is required`)
  return value
}
function allocation(value: unknown): Allocation {
  const input = object(value)
  return { id: text(input, 'id'), runner: text(input, 'runner'), runnerId: text(input, 'runnerId'), provider: text(input, 'provider'), state: text(input, 'state'), threadId: typeof input.threadId === 'string' ? input.threadId : undefined }
}

export default async function (amp: PluginAPI) {
  if (settings.role !== 'operator' || amp.system.executor.kind === 'remote') return

  async function call(operation: string, input: Record<string, unknown> = {}): Promise<unknown> {
    const encoded = JSON.stringify(input)
    const result = settings.config
      ? await amp.$`${settings.binary} --config ${settings.config} api ${operation} --input ${encoded}`
      : await amp.$`${settings.binary} api ${operation} --input ${encoded}`
    if (result.exitCode !== 0) throw new Error(result.stderr.trim() || `ere exited ${result.exitCode}`)
    const parsed: unknown = JSON.parse(result.stdout)
    return parsed
  }

  const tools = [
    ['runner_profiles', 'List runner profiles', 'Listing runner profiles', 'Listed runner profiles'],
    ['runner_plan', 'Plan runner changes', 'Planning runner changes', 'Planned runner changes'],
    ['runner_ensure', 'Prepare runner', 'Preparing runner', 'Prepared runner'],
    ['runner_status', 'Inspect runner', 'Inspecting runner', 'Inspected runner'],
    ['runner_logs', 'Read runner logs', 'Reading runner logs', 'Read runner logs'],
    ['runner_allocations', 'Inspect allocations', 'Inspecting allocations', 'Inspected allocations'],
    ['runner_drain', 'Drain runner', 'Draining runner', 'Drained runner'],
    ['runner_resume', 'Resume runner assignments', 'Resuming runner assignments', 'Resumed runner assignments'],
  ]
  for (const [name, title, active, complete] of tools) {
    amp.registerTool({ name, title, transcriptGroup: { active, complete },
      description: `${title} for declared ere profiles. Draining blocks new managed assignments and retains compute and storage.`,
      inputSchema: { type: 'object', properties: { runner: { type: 'string' }, lines: { type: 'integer', minimum: 1, maximum: 10000 } }, required: name === 'runner_profiles' ? [] : ['runner'], additionalProperties: false },
      async execute(input) {
        if (name !== 'runner_profiles') text(input, 'runner')
        return JSON.stringify(await call(name, input))
      },
    })
  }

  async function report(a: Allocation, threadID: string) {
    const result = await amp.$`amp threads export ${threadID}`
    if (result.exitCode !== 0) throw new Error(`Cannot inspect thread ${threadID}: ${result.stderr}`)
    const exported: unknown = JSON.parse(result.stdout)
    const snapshot = threadSnapshot(exported)
    await call('runner_activity', { runner: a.runner, id: a.id, threadId: threadID, state: snapshot.state })
    return snapshot
  }

  amp.registerTool({
    name: 'runner_run', title: 'Run task on runner',
    transcriptGroup: { active: 'Running runner task', complete: 'Ran runner task' },
    description: 'Allocate a configured runner and submit an independent private Amp thread through the supported CLI. A timeout retains the allocation. Use runner_poll to recover it.',
    inputSchema: { type: 'object', properties: { runner: { type: 'string' }, prompt: { type: 'string' }, mode: { type: 'string' }, title: { type: 'string' }, labels: { type: 'array', items: { type: 'string' } }, features: { type: 'array', items: { type: 'string' } } }, required: ['runner', 'prompt', 'mode'], additionalProperties: false },
    async execute(input) {
      const runner = text(input, 'runner'), prompt = text(input, 'prompt'), mode = text(input, 'mode')
      const a = allocation(await call('runner_thread_create', { ...input, runner, prompt, mode, id: crypto.randomUUID() }))
      if (!a.threadId || !a.threadId.startsWith('T-')) throw new Error(`Allocation ${a.id} retained; recover its thread before retrying`)
      const threadID: `T-${string}` = `T-${a.threadId.slice(2)}`
      try {
        const deadline = Date.now() + settings.waitTimeoutMs
        while (Date.now() < deadline) {
          const snapshot = await report(a, threadID)
          if (snapshot.state === 'idle') return JSON.stringify({ allocation: a.id, runner, threadId: threadID, response: snapshot.response, retained: true })
          if (snapshot.state === 'error') throw new Error('Remote thread failed')
          await new Promise(resolve => setTimeout(resolve, settings.pollIntervalMs))
        }
        throw new Error('Remote thread completion timed out')
      } catch (error) {
        await call('runner_activity', { runner, id: a.id, threadId: threadID, state: 'unknown' })
        return JSON.stringify({ allocation: a.id, runner, threadId: threadID, state: 'unknown', error: String(error), retained: true })
      }
    },
  })

  for (const operation of ['poll', 'release']) {
    amp.registerTool({ name: `runner_${operation}`, title: operation === 'poll' ? 'Check runner task' : 'Release runner allocation',
      description: 'Read current Amp thread activity before updating the allocation. Release requires idle activity and keeps compute and workspace.',
      inputSchema: { type: 'object', properties: { runner: { type: 'string' }, id: { type: 'string' } }, required: ['runner', 'id'], additionalProperties: false },
      async execute(input) {
        const runner = text(input, 'runner'), id = text(input, 'id')
        const record = object(await call('runner_allocations', { runner }))
        if (!Array.isArray(record.allocations)) throw new Error('Invalid allocation list')
        const a = record.allocations.map(allocation).find(item => item.id === id)
        if (!a) throw new Error('Allocation not found')
        if (a.threadId) {
          if (!a.threadId.startsWith('T-')) throw new Error('Invalid Amp thread ID')
          const threadID: `T-${string}` = `T-${a.threadId.slice(2)}`
          const { state } = await report(a, threadID)
          if (operation === 'release' && state !== 'idle') throw new Error(`Thread is ${state}; allocation retained`)
        }
        return JSON.stringify(await call(operation === 'release' ? 'runner_release' : 'runner_allocations', { runner, id }))
      },
    })
  }

  async function profiles() {
    const value = await call('runner_profiles')
    if (!Array.isArray(value)) throw new Error('Invalid profile list')
    return value.map(object)
  }
  for (const operation of ['status', 'logs', 'drain', 'resume']) {
    amp.registerCommand(`runner-${operation}`, { title: `${operation} runner`, category: 'ere', description: `${operation} a configured runner.` }, async ctx => {
      const items = await profiles()
      const name = await ctx.ui.select({ title: 'Choose runner', options: items.map(item => text(item, 'name')) })
      if (!name) return
      await ctx.ui.notify(JSON.stringify(await call(`runner_${operation}`, { runner: name })))
    })
  }
  amp.registerCommand('runner-profiles', { title: 'Choose runner', category: 'ere', description: 'Inspect a configured runner.' }, async ctx => {
    const items = await profiles()
    const name = await ctx.ui.select({ title: 'Choose runner', options: items.map(item => text(item, 'name')) })
    if (name) await ctx.ui.notify(JSON.stringify(await call('runner_status', { runner: name })))
  })
  amp.registerCommand('runner-start', { title: 'Start runner thread', category: 'ere', description: 'Create a private native thread using the current agent mode.' }, async ctx => {
    const items = await profiles()
    const name = await ctx.ui.select({ title: 'Choose runner', options: items.map(item => text(item, 'name')) })
    if (!name) return
    const a = allocation(await call('runner_acquire', { runner: name, id: crypto.randomUUID() }))
    await call('runner_ensure', { runner: name })
    await call('runner_activity', { runner: name, id: a.id, state: 'unknown' })
    const agent = ctx.thread ? await ctx.thread.agent() : amp.getBuiltinAgent('high')
    const thread = await agent.createThread({ executor: { type: 'runner', id: a.runnerId }, visibility: 'private', show: true })
    await call('runner_activity', { runner: name, id: a.id, threadId: thread.id, state: 'unknown' })
    const labeled = await amp.$`amp threads label ${thread.id} ere ${'ere-' + a.provider} ${'ere-allocation-' + a.id}`
    if (labeled.exitCode !== 0) throw new Error(`Thread ${thread.id} created; label failed: ${labeled.stderr}`)
    await ctx.ui.notify(`Runner ${name}; thread ${thread.id}; allocation retained when you leave the TUI.`)
  })
  amp.registerCommand('runner-continue', { title: 'Continue runner thread', category: 'ere', description: 'Reserve a tracked thread and show its continuation link.' }, async ctx => {
    const value = await call('runner_threads')
    if (!Array.isArray(value)) throw new Error('Invalid thread list')
    const threads = value.map(allocation).filter(item => item.threadId)
    const id = await ctx.ui.select({ title: 'Continue thread', options: [...new Set(threads.map(item => item.threadId ?? ''))] })
    if (!id) return
    await call('runner_thread_continue', { threadId: id })
    await ctx.system.open(new URL(`threads/${id}`, amp.system.ampURL))
    await ctx.ui.notify(`Continue in the terminal with: ere continue ${id}`)
  })
  await amp.registerSkill({ path: 'skills/runner-workflow' })
}
