export function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('Expected an object')
  return Object.fromEntries(Object.entries(value))
}

export function threadSnapshot(value: unknown) {
  const thread = record(value), meta = record(thread.meta)
  if (!meta.lastKnownAgentState || !Array.isArray(thread.messages)) return { state: 'unknown', response: undefined }
  const activity = record(meta.lastKnownAgentState)
  if (activity.state === 'running' || activity.state === 'awaiting-approval' || activity.state === 'error') return { state: activity.state, response: undefined }
  const last = thread.messages.at(-1)
  if (!last) return { state: 'unknown', response: undefined }
  const message = record(last)
  const completion = message.state ? record(message.state) : {}
  const idle = activity.state === 'idle' && message.role === 'assistant' && completion.type === 'complete' && completion.stopReason === 'end_turn' && activity.messageID === message.protocolMessageID
  return { state: idle ? 'idle' : 'unknown', response: idle ? message.content : undefined }
}
