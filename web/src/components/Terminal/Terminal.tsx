import { useEffect } from 'react'
import { useTerminal } from '../../hooks/useTerminal'

interface TerminalProps {
  onInput: (data: Uint8Array) => void
  onResize: (cols: number, rows: number) => void
  onReady?: (write: (data: Uint8Array) => void, reset: () => void) => void
}

export default function Terminal({ onInput, onResize, onReady }: TerminalProps) {
  const { containerRef, write, reset } = useTerminal({ onInput, onResize })

  useEffect(() => {
    if (onReady) {
      onReady(write, reset)
    }
  }, [onReady, reset, write])

  return <div className="terminal-container" ref={containerRef} />
}
