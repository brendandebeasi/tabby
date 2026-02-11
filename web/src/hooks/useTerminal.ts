import { useCallback, useEffect, useMemo, useRef } from 'react'
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'

interface UseTerminalOptions {
  readOnly?: boolean
  fontSize?: number
  onInput?: (data: Uint8Array) => void
  onResize?: (cols: number, rows: number) => void
}

export function useTerminal(options: UseTerminalOptions) {
	const containerRef = useRef<HTMLDivElement | null>(null)
	const terminalRef = useRef<Terminal | null>(null)
	const fitAddonRef = useRef<FitAddon | null>(null)
	const optionsRef = useRef<UseTerminalOptions>(options)
	const decoder = useMemo(() => new TextDecoder(), [])
	const encoder = useMemo(() => new TextEncoder(), [])

	useEffect(() => {
		optionsRef.current = options
	}, [options])

	useEffect(() => {
		if (!containerRef.current) {
			return
		}

		if (terminalRef.current) {
			return
		}

		const terminal = new Terminal({
			cursorBlink: true,
			fontSize: options.fontSize ?? 14,
			fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
			disableStdin: options.readOnly ?? false,
			theme: {
				background: '#0b1020',
				foreground: '#e2e8f0'
			}
		})

    const fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
    terminal.open(containerRef.current)
    fitAddon.fit()

    terminalRef.current = terminal
    fitAddonRef.current = fitAddon

		const onDataDisposable = terminal.onData((data) => {
			const handler = optionsRef.current.onInput
			if (handler) {
				handler(encoder.encode(data))
			}
		})

		const handleResize = () => {
			fitAddon.fit()
			const handler = optionsRef.current.onResize
			if (handler) {
				handler(terminal.cols, terminal.rows)
			}
		}

    const observer = new ResizeObserver(handleResize)
    observer.observe(containerRef.current)

    return () => {
      observer.disconnect()
      onDataDisposable.dispose()
      terminal.dispose()
      terminalRef.current = null
    }
	}, [decoder, encoder, options.fontSize, options.readOnly])

  const write = useCallback(
    (data: Uint8Array) => {
      if (!terminalRef.current) {
        return
      }
      terminalRef.current.write(decoder.decode(data, { stream: true }))
    },
    [decoder]
  )

  const reset = useCallback(() => {
    terminalRef.current?.reset()
  }, [])

  return { containerRef, terminalRef, write, reset }
}
