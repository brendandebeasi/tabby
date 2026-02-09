export type ConnectionStatus = 'disconnected' | 'connecting' | 'connected'

export interface ConnectionState {
  status: ConnectionStatus
  error?: string
}

export const initialConnectionState: ConnectionState = {
  status: 'disconnected'
}
