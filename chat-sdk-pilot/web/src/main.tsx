import React, { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { ChatComposer, ChatStream, createChatSessionBinding, useChatSessionBinding, type ChatSessionAttachment, type ChatSessionRole } from '@hollis-labs/kit-chat'
import { createPilotAdapter } from './host'
import './style.css'

function ConversationView({ label, initialRole, initialCursor = '', autoAttach = false }: { label: string; initialRole: ChatSessionRole; initialCursor?: string; autoAttach?: boolean }) {
  const [attachment, setAttachment] = useState<ChatSessionAttachment | null>(null)
  const [desiredRole, setDesiredRole] = useState<ChatSessionRole>(initialRole)
  const [binding] = useState(() => createChatSessionBinding(createPilotAdapter(setAttachment), { cursor: initialCursor }))
  const session = useChatSessionBinding(binding)
  const role = session.role ?? desiredRole

  useEffect(() => { if (autoAttach) void binding.attach(initialRole) }, [])
  useEffect(() => { if (session.role) setDesiredRole(session.role) }, [session.role])
  useEffect(() => {
    if (!attachment) return
    const source = new EventSource(`/api/events/stream?id=${encodeURIComponent(attachment.id)}&after=${encodeURIComponent(session.cursor)}`)
    let fallback: ReturnType<typeof setInterval> | undefined
    source.addEventListener('chat', () => { void binding.refresh() })
    source.onerror = () => {
      source.close()
      if (!fallback) fallback = setInterval(() => void binding.refresh(), 300)
    }
    return () => { source.close(); if (fallback) clearInterval(fallback) }
  }, [attachment, binding])

  return <section className="view" data-testid={`${label.toLowerCase()}-pane`} aria-label={`${label} view`}>
    <header className="view-header">
      <div><h2>{label}</h2><span className="meta" data-testid="role">{role}{session.access === 'detached' ? ' detached' : ' attached'}</span></div>
      <span className="meta" data-testid="cursor">cursor {session.cursor || 'start'}</span>
      <div className="view-actions">
        <button type="button" onClick={() => void binding.attach(role)} disabled={session.access !== 'detached'}>Attach</button>
        <button type="button" onClick={() => void binding.detach()} disabled={session.access === 'detached'}>Detach</button>
        <button type="button" onClick={() => void binding.requestTakeover()} disabled={session.access === 'detached' || session.access === 'controller'}>Take over</button>
      </div>
    </header>
    {session.error && <p role="alert">{session.error}</p>}
    <main><ChatStream items={session.items} aria-label={`${label} transcript`} /></main>
    <footer><ChatComposer onSubmit={text => binding.submit(text)} onDraftChange={binding.setDraft} defaultValue={session.draft} disabled={session.access !== 'controller'} busy={session.pending} placeholder={session.access === 'viewer' ? 'Viewer cannot type' : 'Message or /tool nonzero, /tool lost, /gate'} aria-label={`${label} composer`} footer={session.access === 'viewer' ? <span className="meta">Viewer access is read only. Take over to write.</span> : undefined} /></footer>
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
