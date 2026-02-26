import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import Terminal from './components/Terminal/Terminal'
import SidebarTerminal from './components/Sidebar/SidebarTerminal'
import { useWebSocket } from './hooks/useWebSocket'
import type { ClickableRegion, RenderPayload } from './stores/sidebar'

const TOKEN_KEY = 'tabbyWebToken'
const PANE_KEY = 'tabbyPaneId'
const WS_HOST_KEY = 'tabbyWsHost'
const USER_KEY = 'tabbyAuthUser'
const PASS_KEY = 'tabbyAuthPass'

const MOBILE_MAX_WINDOW_PX = 880
const TABLET_MAX_WINDOW_PX = 1360
const SIDEBAR_WIDTH_MOBILE = 15
const SIDEBAR_WIDTH_TABLET = 20
const SIDEBAR_WIDTH_DESKTOP = 25
const MOBILE_MIN_CONTENT_COLS = 40
const CELL_WIDTH_PX = 8.5

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
  const stored = localStorage.getItem(WS_HOST_KEY)
  if (stored) {
    return stored
  }
  const host = window.location.hostname || '127.0.0.1'
  return `${host}:8080`
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
    localStorage.setItem(PASS_KEY, pass)
    url.searchParams.delete('pass')
    window.history.replaceState({}, '', url.toString())
    return pass
  }
  const stored = localStorage.getItem(PASS_KEY)
  if (stored) {
    return stored
  }
  const legacy = sessionStorage.getItem(PASS_KEY)
  if (legacy) {
    localStorage.setItem(PASS_KEY, legacy)
    return legacy
  }
  return ''
}

function buildWsUrl(token: string | null, wsHost: string | null, user: string, pass: string): string | null {
  if (!token) {
    return null
  }
  const locationHost = window.location.host || '127.0.0.1:8080'
  const inferredHost = wsHost || (window.location.port === '5173' ? `${window.location.hostname}:8080` : locationHost)
  const host = inferredHost.includes(':') ? inferredHost : `${inferredHost}:8080`
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${host}/ws?token=${encodeURIComponent(token)}&user=${encodeURIComponent(user)}&pass=${encodeURIComponent(pass)}`
}

export default function App() {
  const [sidebarOpen, setSidebarOpen] = useState(true)
  const [token, setToken] = useState<string | null>(readToken)
  const [wsHost, setWsHost] = useState<string | null>(readWsHost)
  const [paneId, setPaneId] = useState(readPaneId)
  const [authUser, setAuthUser] = useState(readAuthUser)
  const [authPass, setAuthPass] = useState(readAuthPass)
  const [viewportWidth, setViewportWidth] = useState(() => window.innerWidth)
  const [renderPayload, setRenderPayload] = useState<RenderPayload | null>(null)
  const [writePty, setWritePty] = useState<((data: Uint8Array) => void) | null>(null)
  const [resetPty, setResetPty] = useState<(() => void) | null>(null)
  const [ptyHealth, setPtyHealth] = useState<{ mode: 'streaming' | 'snapshot'; healthy: boolean }>({ mode: 'streaming', healthy: true })
  const sidebarSizeRef = useRef<{ cols: number; rows: number } | null>(null)
  const terminalResizeTimerRef = useRef(0)
  const pendingTerminalResizeRef = useRef<{ paneId: string; cols: number; rows: number } | null>(null)
  const lastTerminalResizeKeyRef = useRef('')
  const lastSidebarResizeKeyRef = useRef('')
  const bootstrapInFlightRef = useRef(false)
  const lastBootstrapAttemptAtRef = useRef(0)

  const wsUrl = useMemo(() => buildWsUrl(token, wsHost, authUser, authPass), [token, wsHost, authUser, authPass])
  const sidebarWidthPx = useMemo(() => {
    let sidebarPercent = SIDEBAR_WIDTH_DESKTOP
    if (viewportWidth <= MOBILE_MAX_WINDOW_PX) {
      sidebarPercent = SIDEBAR_WIDTH_MOBILE
    } else if (viewportWidth <= TABLET_MAX_WINDOW_PX) {
      sidebarPercent = SIDEBAR_WIDTH_TABLET
    }

    const preferredWidth = Math.round((viewportWidth * sidebarPercent) / 100)
    const maxSidebarWidth = Math.max(160, Math.floor(viewportWidth - MOBILE_MIN_CONTENT_COLS * CELL_WIDTH_PX))
    return Math.max(140, Math.min(preferredWidth, maxSidebarWidth))
  }, [viewportWidth])

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

	const handleControlMessage = useCallback((type: string, payload: unknown) => {
		if (type === 'pty_health') {
      const health = payload as { mode?: string; healthy?: boolean } | null
      if (health?.mode === 'streaming' || health?.mode === 'snapshot') {
        setPtyHealth({ mode: health.mode, healthy: health.healthy !== false })
      }
      return
    }
		if (type === 'pty_reset') {
			resetPty?.()
			return
		}
		if (type !== 'attached_pane') {
			return
		}
    const nextPaneId = (payload as { paneId?: string } | null)?.paneId
    if (!nextPaneId || typeof nextPaneId !== 'string') {
      return
    }
    setPaneId((current) => {
      if (current === nextPaneId) {
        return current
      }
      localStorage.setItem(PANE_KEY, nextPaneId)
      return nextPaneId
    })
	}, [resetPty])

  const handlers = useMemo(() => ({
    onSidebarMessage: handleSidebarMessage,
    onControlMessage: handleControlMessage,
    onPtyData: handlePtyData
  }), [handleControlMessage, handleSidebarMessage, handlePtyData])

  const { state, sendJson, sendPtyInput } = useWebSocket(wsUrl, handlers)

  useEffect(() => {
    if (state.status === 'connected' && paneId) {
      sendJson('control', 'attach', { paneId })
    }
  }, [state.status, paneId, sendJson])

  useEffect(() => {
    const onResize = () => setViewportWidth(window.innerWidth)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const bootstrapConnection = useCallback(() => {
    if (!authUser || !authPass) {
      return
    }
    if (bootstrapInFlightRef.current) {
      return
    }
    const now = Date.now()
    if (now-lastBootstrapAttemptAtRef.current < 1200) {
      return
    }
    lastBootstrapAttemptAtRef.current = now
    bootstrapInFlightRef.current = true

    const defaultHost = `${window.location.hostname || '127.0.0.1'}:8080`
    const candidateHosts = [wsHost, defaultHost].filter((value, index, arr): value is string => !!value && arr.indexOf(value) === index)

    const tryBootstrap = async () => {
      for (const host of candidateHosts) {
        try {
          const bootstrapUrl = `http://${host}/bootstrap?user=${encodeURIComponent(authUser)}&pass=${encodeURIComponent(authPass)}`
          const response = await fetch(bootstrapUrl)
          if (!response.ok) {
            continue
          }
          const payload = await response.json() as { token?: string; ws?: string }
          if (payload.token) {
            localStorage.setItem(TOKEN_KEY, payload.token)
            setToken(payload.token)
          }
          if (payload.ws) {
            localStorage.setItem(WS_HOST_KEY, payload.ws)
            setWsHost(payload.ws)
          } else {
            localStorage.setItem(WS_HOST_KEY, host)
            setWsHost(host)
          }
          return
        } catch {
          continue
        }
      }
    }

    tryBootstrap()
      .finally(() => {
        bootstrapInFlightRef.current = false
      })
  }, [authPass, authUser, wsHost])

  useEffect(() => {
    if (!token || state.status === 'disconnected') {
      bootstrapConnection()
    }
  }, [bootstrapConnection, state.status, token])

  useEffect(() => {
    lastTerminalResizeKeyRef.current = ''
    pendingTerminalResizeRef.current = null
  }, [paneId])

  useEffect(() => {
    return () => {
      if (terminalResizeTimerRef.current !== 0) {
        window.clearTimeout(terminalResizeTimerRef.current)
        terminalResizeTimerRef.current = 0
      }
    }
  }, [])

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
    localStorage.setItem(PASS_KEY, value)
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
    const resize = { paneId, cols, rows }
    pendingTerminalResizeRef.current = resize

    const resizeKey = `${paneId}:${cols}:${rows}`
    if (lastTerminalResizeKeyRef.current === resizeKey) {
      return
    }

    if (terminalResizeTimerRef.current !== 0) {
      return
    }

    terminalResizeTimerRef.current = window.setTimeout(() => {
      terminalResizeTimerRef.current = 0
      const next = pendingTerminalResizeRef.current
      if (!next) {
        return
      }
      const nextKey = `${next.paneId}:${next.cols}:${next.rows}`
      if (lastTerminalResizeKeyRef.current === nextKey) {
        return
      }
      lastTerminalResizeKeyRef.current = nextKey
      sendJson('control', 'resize', next)
    }, 120)
  }

  const handleSidebarResize = (cols: number, rows: number) => {
    sidebarSizeRef.current = { cols, rows }
    const resizeKey = `${cols}:${rows}`
    if (lastSidebarResizeKeyRef.current === resizeKey) {
      return
    }
    lastSidebarResizeKeyRef.current = resizeKey
    sendJson('sidebar', 'resize', { width: cols, height: rows, color_profile: 'TrueColor' })
  }

  useEffect(() => {
    if (state.status !== 'connected') return
    const size = sidebarSizeRef.current
    if (size) {
      sendJson('sidebar', 'resize', { width: size.cols, height: size.rows, color_profile: 'TrueColor' })
    }
  }, [state.status, sendJson])

  useEffect(() => {
    if (state.status !== 'connected') {
      return
    }
    const interval = window.setInterval(() => {
      const size = sidebarSizeRef.current
      if (!size) {
        return
      }
      sendJson('sidebar', 'resize', { width: size.cols, height: size.rows, color_profile: 'TrueColor' })
    }, 1500)
    return () => window.clearInterval(interval)
  }, [state.status, sendJson])

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
        <button onClick={() => setSidebarOpen(o => !o)}>Sidebar</button>
        <span className={`status ${state.status === 'connected' ? 'connected' : ''}`}>
          {state.status}
        </span>
        <span className={`status pty-mode ${ptyHealth.mode === 'snapshot' ? 'snapshot' : 'streaming'}`}>
          {ptyHealth.mode}
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
        <aside
          className={`sidebar-panel${sidebarOpen ? '' : ' collapsed'}`}
          style={{
            width: sidebarOpen ? `${sidebarWidthPx}px` : '0px',
            ...(renderPayload?.sidebar_bg ? { background: renderPayload.sidebar_bg } : {})
          }}
        >
          <SidebarTerminal
            content={renderPayload?.content ?? ''}
            regions={renderPayload?.regions ?? []}
            onRegionClick={handleRegionClick}
            onResize={handleSidebarResize}
            backgroundColor={renderPayload?.sidebar_bg}
          />
        </aside>

        <div className="terminal-shell">
          <Terminal
            onInput={handleTerminalInput}
            onResize={handleTerminalResize}
            onReady={(writer, resetter) => {
              setWritePty(() => writer)
              setResetPty(() => resetter)
            }}
          />
        </div>
      </div>
    </div>
  )
}
