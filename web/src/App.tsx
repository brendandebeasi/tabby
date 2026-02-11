import { useCallback, useEffect, useMemo, useState } from 'react'
import Terminal from './components/Terminal/Terminal'
import SidebarDrawer from './components/Sidebar/SidebarDrawer'
import SidebarTerminal from './components/Sidebar/SidebarTerminal'
import { useWebSocket } from './hooks/useWebSocket'
import type { ClickableRegion, RenderPayload } from './stores/sidebar'

const TOKEN_KEY = 'tabbyWebToken'
const PANE_KEY = 'tabbyPaneId'
const WS_HOST_KEY = 'tabbyWsHost'
const USER_KEY = 'tabbyAuthUser'
const PASS_KEY = 'tabbyAuthPass'

function readToken(): string | null {
  const url = new URL(window.location.href)
  const token = url.searchParams.get('token')
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
    url.searchParams.delete('token')
    window.history.replaceState({}, '', url.toString())
    return token
  }
  return localStorage.getItem(TOKEN_KEY)
}

function readPaneId(): string {
  const url = new URL(window.location.href)
  const pane = url.searchParams.get('pane')
  if (pane) {
    localStorage.setItem(PANE_KEY, pane)
    url.searchParams.delete('pane')
    window.history.replaceState({}, '', url.toString())
    return pane
  }
  return localStorage.getItem(PANE_KEY) ?? ''
}

function readWsHost(): string | null {
  const url = new URL(window.location.href)
  const wsHost = url.searchParams.get('ws')
  if (wsHost) {
    localStorage.setItem(WS_HOST_KEY, wsHost)
    url.searchParams.delete('ws')
    window.history.replaceState({}, '', url.toString())
    return wsHost
  }
  return localStorage.getItem(WS_HOST_KEY)
}

function readAuthUser(): string {
  const url = new URL(window.location.href)
  const user = url.searchParams.get('user')
  if (user) {
    localStorage.setItem(USER_KEY, user)
    url.searchParams.delete('user')
    window.history.replaceState({}, '', url.toString())
    return user
  }
  return localStorage.getItem(USER_KEY) ?? ''
}

function readAuthPass(): string {
  const url = new URL(window.location.href)
  const pass = url.searchParams.get('pass')
  if (pass) {
    sessionStorage.setItem(PASS_KEY, pass)
    url.searchParams.delete('pass')
    window.history.replaceState({}, '', url.toString())
    return pass
  }
  return sessionStorage.getItem(PASS_KEY) ?? ''
}

function buildWsUrl(token: string | null, wsHost: string | null, user: string, pass: string): string | null {
  if (!token) {
    return null
  }
  const host = wsHost || window.location.host || 'localhost:8080'
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${host}/ws?token=${encodeURIComponent(token)}&user=${encodeURIComponent(user)}&pass=${encodeURIComponent(pass)}`
}

export default function App() {
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [paneId, setPaneId] = useState(readPaneId)
  const [authUser, setAuthUser] = useState(readAuthUser)
  const [authPass, setAuthPass] = useState(readAuthPass)
  const [renderPayload, setRenderPayload] = useState<RenderPayload | null>(null)
  const [writePty, setWritePty] = useState<((data: Uint8Array) => void) | null>(null)

  const token = useMemo(() => readToken(), [])
  const wsHost = useMemo(() => readWsHost(), [])
  const wsUrl = useMemo(() => buildWsUrl(token, wsHost, authUser, authPass), [token, wsHost, authUser, authPass])

  const handleSidebarMessage = useCallback((type: string, payload: unknown) => {
    if (type === 'render') {
      setRenderPayload(payload as RenderPayload)
    }
  }, [])

  const handlePtyData = useCallback((incomingPaneId: string, data: Uint8Array) => {
    if (!writePty || incomingPaneId !== paneId) {
      return
    }
    writePty(data)
  }, [paneId, writePty])

  const handlers = useMemo(() => ({
    onSidebarMessage: handleSidebarMessage,
    onPtyData: handlePtyData
  }), [handleSidebarMessage, handlePtyData])

  const { state, sendJson, sendPtyInput } = useWebSocket(wsUrl, handlers)

  useEffect(() => {
    if (state.status === 'connected' && paneId) {
      sendJson('control', 'attach', { paneId })
    }
  }, [state.status, paneId, sendJson])

  const attachPane = useCallback(() => {
    if (!paneId) {
      return
    }
    sendJson('control', 'attach', { paneId })
    localStorage.setItem(PANE_KEY, paneId)
  }, [paneId, sendJson])

  const detachPane = useCallback(() => {
    if (!paneId) {
      return
    }
    sendJson('control', 'detach', { paneId })
  }, [paneId, sendJson])

  const handlePaneChange = (value: string) => {
    setPaneId(value)
  }

  const handleUserChange = (value: string) => {
    setAuthUser(value)
    localStorage.setItem(USER_KEY, value)
  }

  const handlePassChange = (value: string) => {
    setAuthPass(value)
    sessionStorage.setItem(PASS_KEY, value)
  }

  const handleTerminalInput = (data: Uint8Array) => {
    if (!paneId) {
      return
    }
    sendPtyInput(paneId, data)
  }

  const handleTerminalResize = (cols: number, rows: number) => {
    if (!paneId) {
      return
    }
    sendJson('control', 'resize', { paneId, cols, rows })
  }

  const handleSidebarResize = (cols: number, rows: number) => {
    sendJson('sidebar', 'resize', { width: cols, height: rows, color_profile: 'TrueColor' })
  }

  const handleRegionClick = (region: ClickableRegion) => {
    if (!renderPayload) {
      return
    }
    sendJson('sidebar', 'input', {
      seq: renderPayload.seq,
      type: 'action',
      resolved_action: region.action,
      resolved_target: region.target,
      is_touch_mode: true
    })
  }

  return (
    <div className="app">
      <header className="header">
        <button onClick={() => setSidebarOpen(true)}>Sidebar</button>
        <span className={`status ${state.status === 'connected' ? 'connected' : ''}`}>
          {state.status}
        </span>
        <input
          value={authUser}
          onChange={(event) => handleUserChange(event.target.value)}
          placeholder="user"
        />
        <input
          value={authPass}
          onChange={(event) => handlePassChange(event.target.value)}
          placeholder="password"
          type="password"
        />
        <input
          value={paneId}
          onChange={(event) => handlePaneChange(event.target.value)}
          placeholder="pane id"
        />
        <button onClick={attachPane} disabled={!paneId}>Attach</button>
        <button onClick={detachPane} disabled={!paneId}>Detach</button>
      </header>

      <div className="layout">
        <SidebarDrawer open={sidebarOpen} onClose={() => setSidebarOpen(false)}>
          <SidebarTerminal
            content={renderPayload?.content ?? ''}
            regions={renderPayload?.regions ?? []}
            onRegionClick={handleRegionClick}
            onResize={handleSidebarResize}
          />
        </SidebarDrawer>

        <div className="terminal-shell">
          <Terminal
            onInput={handleTerminalInput}
            onResize={handleTerminalResize}
            onReady={(writer) => setWritePty(() => writer)}
          />
        </div>
      </div>
    </div>
  )
}
