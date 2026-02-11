import { useCallback, useEffect, useRef, useState } from 'react'
import { decodePtyFrame, encodePtyInput, PTY_FRAME_DATA } from '../utils/protocol'
import { initialConnectionState, type ConnectionState } from '../stores/connection'

interface WebSocketHandlers {
  onSidebarMessage?: (type: string, payload: unknown) => void
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

    setState({ status: 'connecting' })
    const ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'
    socketRef.current = ws

    ws.onopen = () => {
      setState({ status: 'connected' })
    }

    ws.onerror = () => {
      setState({ status: 'disconnected', error: 'websocket error' })
    }

    ws.onclose = () => {
      setState({ status: 'disconnected' })
    }

    ws.onmessage = (event) => {
		if (typeof event.data === 'string') {
			try {
				const msg = JSON.parse(event.data) as { channel: string; type: string; payload: unknown }
				if (msg.channel === 'sidebar') {
					handlersRef.current.onSidebarMessage?.(msg.type, msg.payload)
				}
			} catch {
				return
			}
        return
      }

		if (event.data instanceof ArrayBuffer) {
			const frame = decodePtyFrame(event.data)
			if (frame && frame.type === PTY_FRAME_DATA) {
				handlersRef.current.onPtyData?.(frame.paneId, frame.data)
			}
		}
	}

    return () => {
      ws.close()
      socketRef.current = null
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
