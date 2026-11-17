import React, { useCallback, useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { ChatComposer, ChatStream, createChatStreamBinding } from '@hollis-labs/kit-chat'
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
    // Project the host's committed event through the merged chatstream binding.
    // The host cursor remains the durable position for replay and attachment.
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
  if (event.kind === 'notice') return { kind: 'marker', id: `event-${event.cursor}`, label: event.text || 'Notice' }
  return null
}

function cardItem(card: Card, id: string): Item | null {
  if (card.envelope) return { kind: 'card', wireKind: 'tool', id, content: <ToolCard toolName="local_command" envelope={card.envelope as never} /> }
  if (card.kind === 'gate' && card.ticket_id && card.decision_id) {
    return { kind: 'card', wireKind: 'approval', id, content: <GateApprovalCard enabled state={{ kind: 'approved', scope: 'once', decisionId: card.decision_id }} ticketId={card.ticket_id} ticketRevision={card.ticket_revision || 1} operationDigest={JSON.stringify(card.operation_digest)} title="Fake Gatecrasher approval" description="Decision recorded; no effect executed." risk="low" options={[]} onSelect={() => {}} onDismiss={() => {}} /> }
  }
  return null
}

function ConversationView({ label, initialRole, initialCursor = '', autoAttach = false }: { label: string; initialRole: Role; initialCursor?: string; autoAttach?: boolean }) {
  const [role, setRole] = useState<Role>(initialRole)
  const [attachment, setAttachment] = useState<Attachment | null>(null)
  const [rows, setRows] = useState<Item[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [cursorLabel, setCursorLabel] = useState(initialCursor)
  const cursor = useRef(initialCursor)
  const seen = useRef(new Set<string>())

  const ingest = useCallback((events: Event[]) => {
    const items: Item[] = []
    for (const event of events) {
      if (seen.current.has(event.cursor)) continue
      seen.current.add(event.cursor)
      cursor.current = event.cursor
      setCursorLabel(event.cursor)
      const item = project(event)
      if (item) items.push(item)
    }
    if (items.length) setRows(previous => [...previous, ...items])
  }, [])

  const attach = useCallback(async (nextRole: Role): Promise<Attachment | undefined> => {
    try {
      const result = await api('attach', { role: nextRole, after: cursor.current })
      setAttachment(result.attachment)
      setRole(nextRole)
      ingest(result.events || [])
      setError('')
      return result.attachment as Attachment
    } catch (cause) { setError(String(cause)); return undefined }
  }, [ingest])

  const poll = useCallback(async (current: Attachment) => {
    try {
      const result = await api(`events?id=${encodeURIComponent(current.id)}&after=${encodeURIComponent(cursor.current)}`)
      ingest(result.events || [])
    } catch (cause) { setError(String(cause)) }
  }, [ingest])

  useEffect(() => { if (autoAttach) void attach(initialRole) }, [])
  useEffect(() => {
    if (!attachment) return
    const source = new EventSource(`/api/events/stream?id=${encodeURIComponent(attachment.id)}&after=${encodeURIComponent(cursor.current)}`)
    let fallback: ReturnType<typeof setInterval> | undefined
    source.addEventListener('chat', message => {
      try { ingest([JSON.parse((message as MessageEvent).data) as Event]) }
      catch { setError('Invalid event stream payload') }
    })
    source.onerror = () => {
      source.close()
      if (!fallback) fallback = setInterval(() => void poll(attachment), 300)
    }
    return () => { source.close(); if (fallback) clearInterval(fallback) }
  }, [attachment, ingest, poll])

  const detach = async () => {
    if (!attachment) return
    try { await api('detach', { id: attachment.id }); setAttachment(null); setError('') }
    catch (cause) { setError(String(cause)) }
  }
  const takeover = async () => {
    if (!attachment) return
    try {
      let target = attachment
      if (role === 'viewer') {
        await api('detach', { id: attachment.id })
        setAttachment(null)
        const promoted = await attach('controller')
        if (!promoted) return
        target = promoted
      }
      await api('takeover', { id: target.id })
      setRole('controller')
      setError('')
    } catch (cause) { setError(String(cause)) }
  }
  const send = async (text: string): Promise<boolean> => {
    if (!attachment || role !== 'controller') return false
    setBusy(true)
    try {
      const result = await api('send', { id: attachment.id, text })
      await poll(attachment)
      const card = result.card && cardItem(result.card, `card-${result.card.tool_call_id || result.card.ticket_id}`)
      if (card) setRows(previous => [...previous, card])
      setError('')
      return true
    } catch (cause) { setError(String(cause)); return false }
    finally { setBusy(false) }
  }

  return <section className="view" data-testid={`${label.toLowerCase()}-pane`} aria-label={`${label} view`}>
    <header className="view-header">
      <div><h2>{label}</h2><span className="meta" data-testid="role">{role}{attachment ? ' attached' : ' detached'}</span></div>
      <span className="meta" data-testid="cursor">cursor {cursorLabel || 'start'}</span>
      <div className="view-actions">
        <button type="button" onClick={() => void attach(role)} disabled={!!attachment}>Attach</button>
        <button type="button" onClick={() => void detach()} disabled={!attachment}>Detach</button>
        <button type="button" onClick={() => void takeover()} disabled={!attachment}>Take over</button>
      </div>
    </header>
    {error && <p role="alert">{error}</p>}
    <main><ChatStream items={rows as never} aria-label={`${label} transcript`} /></main>
    <footer><ChatComposer onSubmit={send} disabled={!attachment || role !== 'controller'} busy={busy} placeholder={role === 'viewer' ? 'Viewer cannot type' : 'Message or /tool nonzero, /tool lost, /gate'} aria-label={`${label} composer`} footer={role === 'viewer' ? <span className="meta">Viewer access is read only. Take over to write.</span> : undefined} /></footer>
  </section>
}

function App() {
  const query = new URLSearchParams(location.search)
  return <div className="pilot">
    <header className="pilot-header"><h1>Chat SDK pilot</h1><p>Two views of one durable conversation. The controller writes; the viewer follows and can take over.</p></header>
    <div className="views">
      <ConversationView label="Controller" initialRole="controller" autoAttach />
      <ConversationView label="Viewer" initialRole="viewer" initialCursor={query.get('after') || '1'} />
    </div>
  </div>
}

createRoot(document.getElementById('root')!).render(<App />)
