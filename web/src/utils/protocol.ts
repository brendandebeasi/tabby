export const PTY_FRAME_DATA = 0x01
export const PTY_FRAME_INPUT = 0x02

export interface WebSocketMessage<T = unknown> {
  channel: 'sidebar' | 'control'
  type: string
  payload: T
}

export interface PtyFrame {
  type: number
  paneId: string
  data: Uint8Array
}

export function encodePtyInput(paneId: string, data: Uint8Array): Uint8Array {
  const paneBytes = new TextEncoder().encode(paneId)
  const frame = new Uint8Array(1 + paneBytes.length + 1 + data.length)
  frame[0] = PTY_FRAME_INPUT
  frame.set(paneBytes, 1)
  frame[1 + paneBytes.length] = 0x00
  frame.set(data, 1 + paneBytes.length + 1)
  return frame
}

export function decodePtyFrame(buffer: ArrayBuffer): PtyFrame | null {
  const bytes = new Uint8Array(buffer)
  if (bytes.length < 3) {
    return null
  }
  const frameType = bytes[0]
  const sepIndex = bytes.indexOf(0x00, 1)
  if (sepIndex <= 1) {
    return null
  }
  const paneId = new TextDecoder().decode(bytes.slice(1, sepIndex))
  const data = bytes.slice(sepIndex + 1)
  return { type: frameType, paneId, data }
}
