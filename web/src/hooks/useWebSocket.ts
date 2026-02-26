import { useCallback, useEffect, useRef, useState } from 'react'
import { decodePtyFrame, encodePtyInput, PTY_FRAME_DATA } from '../utils/protocol'
import { initialConnectionState, type ConnectionState } from '../stores/connection'

interface WebSocketHandlers {
  onSidebarMessage?: (type: string, payload: unknown) => void
  onControlMessage?: (type: string, payload: unknown) => void
  onPtyData?: (paneId: string, data: Uint8Array) => void
}

interface WebSocketHook {
  state: ConnectionState
  sendJson: (channel: 'sidebar' | 'control', type: string, payload: unknown) => void
  sendPtyInput: (paneId: string, data: Uint8Array) => void
}

export function useWebSocket(url: string | null, handlers: WebSocketHandlers): WebSocketHook {
	const [state, setState] = useState<ConnectionState>(initialConnectionState)
	const socketRef = useRef<WebSocket | null>(null)
	const handlersRef = useRef<WebSocketHandlers>(handlers)

	useEffect(() => {
		handlersRef.current = handlers
	}, [handlers])

	useEffect(() => {
		if (!url) {
			return
		}

		let closed = false
		let reconnectTimer: number | null = null
		let reconnectAttempts = 0

		const clearReconnectTimer = () => {
			if (reconnectTimer !== null) {
				window.clearTimeout(reconnectTimer)
				reconnectTimer = null
			}
		}

		const scheduleReconnect = () => {
			if (closed) {
				return
			}
			clearReconnectTimer()
			const delay = Math.min(3000, 400+reconnectAttempts*400)
			reconnectTimer = window.setTimeout(() => {
				reconnectTimer = null
				connect()
			}, delay)
		}

		const connect = () => {
			if (closed) {
				return
			}
			setState({ status: 'connecting' })
			const ws = new WebSocket(url)
			ws.binaryType = 'arraybuffer'
			socketRef.current = ws

			ws.onopen = () => {
				reconnectAttempts = 0
				setState({ status: 'connected' })
			}

			ws.onerror = () => {
				setState({ status: 'disconnected', error: 'websocket error' })
			}

			ws.onclose = () => {
				if (socketRef.current === ws) {
					socketRef.current = null
				}
				setState({ status: 'disconnected' })
				reconnectAttempts += 1
				scheduleReconnect()
			}

			ws.onmessage = (event) => {
				if (typeof event.data === 'string') {
					try {
						const msg = JSON.parse(event.data) as { channel: string; type: string; payload: unknown }
				if (msg.channel === 'sidebar') {
					handlersRef.current.onSidebarMessage?.(msg.type, msg.payload)
				} else if (msg.channel === 'control') {
					handlersRef.current.onControlMessage?.(msg.type, msg.payload)
				}
			} catch {
				return
			}
        return
      }

		const handleBinaryFrame = (buffer: ArrayBuffer) => {
			const frame = decodePtyFrame(buffer)
			if (frame && frame.type === PTY_FRAME_DATA) {
				handlersRef.current.onPtyData?.(frame.paneId, frame.data)
			}
		}

		if (event.data instanceof ArrayBuffer) {
			handleBinaryFrame(event.data)
			return
				}

				if (event.data instanceof Blob) {
					event.data.arrayBuffer().then(handleBinaryFrame).catch(() => undefined)
				}
			}
		}

		connect()

		return () => {
			closed = true
			clearReconnectTimer()
			const ws = socketRef.current
			socketRef.current = null
			if (ws) {
				ws.close()
			}
		}
	}, [url])

  const sendJson = useCallback((channel: 'sidebar' | 'control', type: string, payload: unknown) => {
    const ws = socketRef.current
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return
    }
    ws.send(JSON.stringify({ channel, type, payload }))
  }, [])

  const sendPtyInput = useCallback((paneId: string, data: Uint8Array) => {
    const ws = socketRef.current
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      return
    }
    ws.send(encodePtyInput(paneId, data))
  }, [])

  return { state, sendJson, sendPtyInput }
}
