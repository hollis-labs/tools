import React from 'react'
import { createChatStreamBinding, type ChatItem, type ChatSessionAdapter, type ChatSessionAttachment, type ChatSessionEvent, type ChatSessionLease, type ChatSessionRole } from '@hollis-labs/kit-chat'
import { GateApprovalCard, ToolCard } from '@hollis-labs/kit-cards'

type HostEvent = { cursor: string; kind: 'input' | 'output' | 'notice' | 'approval'; id: string; text?: string; reference?: { digest?: { value?: string } } }
type HostLease = { ID?: string; Writer?: string; ExpiresAt?: string; id?: string; writer_id?: string; expires_at?: string }
type Card = { kind?: string; tool_call_id?: string; envelope?: Record<string, unknown>; ticket_id?: string; ticket_revision?: number; decision_id?: string; operation_digest?: unknown; state?: string }

export async function api(path: string, body?: unknown) {
  const response = await fetch('/api/' + path, body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const value = await response.json()
  if (!response.ok) throw new Error(value.error || `HTTP ${response.status}`)
  return value
}

function project(event: HostEvent): ChatItem {
  if (event.kind === 'input') return { kind: 'message', id: `event-${event.cursor}`, role: 'user', content: event.text || '' }
  if (event.kind === 'output') {
    // Keep the merged chatstream binding as the projection of runtime output.
    // The chathost cursor, not a local row index, is the replay position.
    const binding = createChatStreamBinding({ scheduleFrame: flush => { flush(); return () => {} } })
    const base = { v: '1', run_id: event.id, time: new Date().toISOString() }
    const feed = (seq: number, verb: string, fields: Record<string, unknown> = {}) => binding.consume({ ...base, seq, verb, ...fields } as never)
    feed(1, 'run.start')
    feed(2, 'message.start', { message_id: event.id, role: 'assistant' })
    feed(3, 'part.start', { part_id: 'text', kind: 'text' })
    feed(4, 'part.delta', { part_id: 'text', text: event.text || '' })
    feed(5, 'part.end', { part_id: 'text' })
    feed(6, 'message.end', { message_id: event.id })
    feed(7, 'run.finish', { reason: 'stop' })
    const item = binding.getSnapshot().items[0]
    if (item) return { ...item, id: `event-${event.cursor}` }
  }
  return { kind: 'marker', id: `event-${event.cursor}`, variant: 'info', label: event.text || 'Conversation notice' }
}

function cardItem(card: Card, id: string): ChatItem | null {
  if (card.envelope) return { kind: 'card', wireKind: 'tool', id, content: <ToolCard toolName="local_command" envelope={card.envelope as never} /> }
  if (card.kind === 'gate' && card.ticket_id && card.decision_id) {
    return { kind: 'card', wireKind: 'approval', id, content: <GateApprovalCard enabled state={{ kind: 'approved', scope: 'once', decisionId: card.decision_id }} ticketId={card.ticket_id} ticketRevision={card.ticket_revision || 1} operationDigest={JSON.stringify(card.operation_digest)} title="Fake Gatecrasher approval" description="Decision recorded; no effect executed." risk="low" options={[]} onSelect={() => {}} onDismiss={() => {}} /> }
  }
  return null
}

function leaseView(value: HostLease): ChatSessionLease {
  return {
    id: value.id || value.ID || '',
    writerId: value.writer_id || value.Writer || '',
    expiresAt: new Date(value.expires_at || value.ExpiresAt || 0).getTime(),
  }
}

/** Thin HTTP adapter; chathost and its binding remain the admission authority. */
export function createPilotAdapter(onAttachment: (attachment: ChatSessionAttachment | null) => void): ChatSessionAdapter {
  let current: ChatSessionAttachment | null = null
  return {
    async attach(role: ChatSessionRole) {
      const result = await api('attach', { role, after: '' })
      current = result.attachment as ChatSessionAttachment
      onAttachment(current)
      return current
    },
    async detach(attachment) {
      await api('detach', { id: attachment.id })
      if (current?.id === attachment.id) { current = null; onAttachment(null) }
    },
    async readEvents(attachment, after): Promise<readonly ChatSessionEvent[]> {
      const result = await api(`events?id=${encodeURIComponent(attachment.id)}&after=${encodeURIComponent(after)}`)
      return Promise.all((result.events as HostEvent[]).map(async event => {
        let item: ChatItem = project(event)
        const digest = event.reference?.digest?.value
        if ((event.kind === 'notice' || event.kind === 'approval') && digest) {
          const response = await api(`card?id=${encodeURIComponent(attachment.id)}&hash=${encodeURIComponent(digest)}`)
          item = cardItem((response.card ?? response) as Card, `card-${event.cursor}`) ?? item
        }
        return { kind: 'item' as const, cursor: event.cursor, id: event.id, item }
      }))
    },
    async acquire(attachment) {
      const result = await api('acquire', { id: attachment.id })
      return leaseView(result.lease as HostLease)
    },
    async takeover(attachment) {
      const result = await api('takeover', { id: attachment.id })
      return leaseView(result.lease as HostLease)
    },
    async submit(lease, inputId, text) {
      const attachment = current
      if (!attachment) throw new Error('Detached')
      const result = await api('send', { id: attachment.id, input_id: inputId, lease_id: lease.id, lease_writer: lease.writerId, text })
      return String(result.cursor)
    },
  }
}
