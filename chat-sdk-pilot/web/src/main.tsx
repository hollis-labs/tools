import React, { useCallback, useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { ChatInput, ChatStream, createChatStreamBinding } from '@hollis-labs/kit-chat'
import { GateApprovalCard, ToolCard } from '@hollis-labs/kit-cards'
import './style.css'

type Role = 'viewer' | 'controller'
type Event = { cursor: string; kind: 'input' | 'output' | 'notice' | 'approval'; id: string; text?: string }
type Attachment = { id: string; role: Role }
type Card = { kind?: string; tool_call_id?: string; envelope?: Record<string, unknown>; ticket_id?: string; ticket_revision?: number; decision_id?: string; operation_digest?: unknown; state?: string }
type Item = { kind: 'message' | 'card' | 'marker'; id: string; role?: string; content?: React.ReactNode; wireKind?: string; variant?: string; label?: string }

async function api(path: string, body?: unknown) {
  const response = await fetch('/api/' + path, body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const value = await response.json()
  if (!response.ok) throw new Error(value.error || `HTTP ${response.status}`)
  return value
}

function project(event: Event): Item | null {
  if (event.kind === 'input') return { kind: 'message', id: `event-${event.cursor}`, role: 'user', content: event.text || '' }
  if (event.kind === 'output') {
    // The kit-chat binding consumes the same ordered chatstream vocabulary as
    // production adapters. The host event cursor remains the durable position.
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
    const item = binding.getSnapshot().items[0] as Item | undefined
    return item ? { ...item, id: `event-${event.cursor}` } : null
  }
  return null
}

function cardItem(card: Card, id: string): Item | null {
  if (card.envelope) return { kind: 'card', wireKind: 'tool', id, content: <ToolCard toolName="local_command" envelope={card.envelope as never} /> }
  if (card.kind === 'gate' && card.ticket_id && card.decision_id) {
    return { kind: 'card', wireKind: 'approval', id, content: <GateApprovalCard enabled state={{ kind: 'approved', scope: 'once', decisionId: card.decision_id }} ticketId={card.ticket_id} ticketRevision={card.ticket_revision || 1} operationDigest={JSON.stringify(card.operation_digest)} title="Fake Gatecrasher approval" description="Decision recorded; no effect executed." risk="low" options={[]} onSelect={() => {}} onDismiss={() => {}} /> }
  }
  return null
}

function App() {
  const query = new URLSearchParams(location.search)
  const initialRole: Role = query.get('role') === 'viewer' ? 'viewer' : 'controller'
  const [role, setRole] = useState<Role>(initialRole)
  const [attachment, setAttachment] = useState<Attachment | null>(null)
  const [rows, setRows] = useState<Item[]>([])
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const cursor = useRef(query.get('after') || '')
  const seen = useRef(new Set<string>())

  const ingest = useCallback((events: Event[]) => {
    const items: Item[] = []
    for (const event of events) {
      if (seen.current.has(event.cursor)) continue
      seen.current.add(event.cursor)
      cursor.current = event.cursor
      const item = project(event)
      if (item) items.push(item)
    }
    if (items.length) setRows(previous => [...previous, ...items])
  }, [])

  const attach = useCallback(async (nextRole: Role) => {
    try {
      const result = await api('attach', { role: nextRole, after: cursor.current })
      setAttachment(result.attachment)
      setRole(nextRole)
      ingest(result.events || [])
      setError('')
    } catch (cause) { setError(String(cause)) }
  }, [ingest])

  const poll = useCallback(async () => {
    if (!attachment) return
    try {
      const result = await api(`events?id=${encodeURIComponent(attachment.id)}&after=${encodeURIComponent(cursor.current)}`)
      ingest(result.events || [])
    } catch (cause) { setError(String(cause)) }
  }, [attachment, ingest])

  useEffect(() => { void attach(initialRole) }, [])
  useEffect(() => { if (!attachment) return; const timer = setInterval(() => void poll(), 300); return () => clearInterval(timer) }, [attachment, poll])

  const detach = async () => {
    if (!attachment) return
    try { await api('detach', { id: attachment.id }); setAttachment(null); setError('') } catch (cause) { setError(String(cause)) }
  }
  const takeover = async () => {
    if (!attachment) return
    try { await api('takeover', { id: attachment.id }); setError('') } catch (cause) { setError(String(cause)) }
  }
  const send = async (text: string) => {
    if (!attachment || role !== 'controller' || !text.trim()) return
    setBusy(true)
    try {
      const result = await api('send', { id: attachment.id, text })
      setDraft('')
      await poll()
      const card = result.card && cardItem(result.card, `card-${result.card.tool_call_id || result.card.ticket_id}`)
      if (card) setRows(previous => [...previous, card])
      setError('')
    } catch (cause) { setError(String(cause)) }
    finally { setBusy(false) }
  }

  return <div className="pilot">
    <header>
      <h1>Chat SDK pilot</h1>
      <span className="meta" data-testid="role">{role}{attachment ? ' attached' : ' detached'}</span>
      <span className="meta" data-testid="cursor">cursor {cursor.current || 'start'}</span>
      <button type="button" onClick={() => void attach(role)} disabled={!!attachment}>Attach</button>
      <button type="button" onClick={() => void detach()} disabled={!attachment}>Detach</button>
      <button type="button" onClick={() => void takeover()} disabled={!attachment || role !== 'controller'}>Take over</button>
    </header>
    {error && <p role="alert">{error}</p>}
    <main><ChatStream items={rows as never} aria-label="Pilot transcript" /></main>
    <footer><ChatInput value={draft} onValueChange={setDraft} onSubmit={send} disabled={!attachment || role !== 'controller'} busy={busy} placeholder={role === 'viewer' ? 'Viewer cannot type' : 'Message or /tool nonzero, /tool lost, /gate'} /></footer>
  </div>
}

createRoot(document.getElementById('root')!).render(<App />)
