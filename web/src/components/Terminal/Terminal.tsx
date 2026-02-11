import { useEffect } from 'react'
import { useTerminal } from '../../hooks/useTerminal'

interface TerminalProps {
  onInput: (data: Uint8Array) => void
  onResize: (cols: number, rows: number) => void
  onReady?: (write: (data: Uint8Array) => void) => void
}

export default function Terminal({ onInput, onResize, onReady }: TerminalProps) {
  const { containerRef, write } = useTerminal({ onInput, onResize })

  useEffect(() => {
    if (onReady) {
      onReady(write)
    }
  }, [onReady, write])

  return <div className="terminal-container" ref={containerRef} />
}
