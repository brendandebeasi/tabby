import { useCallback, useEffect, useMemo, useRef } from 'react'
import { Terminal } from 'xterm'
import { FitAddon } from 'xterm-addon-fit'

interface UseTerminalOptions {
  readOnly?: boolean
  fontSize?: number
  backgroundColor?: string
  onInput?: (data: Uint8Array) => void
  onResize?: (cols: number, rows: number) => void
}

export function useTerminal(options: UseTerminalOptions) {
	const containerRef = useRef<HTMLDivElement | null>(null)
	const terminalRef = useRef<Terminal | null>(null)
	const optionsRef = useRef<UseTerminalOptions>(options)
	const encoder = useMemo(() => new TextEncoder(), [])
	const decoderRef = useRef<TextDecoder>(new TextDecoder())
	const writeQueueRef = useRef<Uint8Array[]>([])
	const flushFrameRef = useRef(0)

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
				background: options.backgroundColor ?? '#0b1020',
				foreground: '#e2e8f0'
			}
		})

    const fitAddon = new FitAddon()
    terminal.loadAddon(fitAddon)
		terminal.open(containerRef.current)
		fitAddon.fit()

		terminalRef.current = terminal
		let resizeFrame = 0
		let lastNotifiedCols = terminal.cols
		let lastNotifiedRows = terminal.rows

    // Notify caller of initial dimensions immediately after fit
    if (options.onResize) {
      options.onResize(terminal.cols, terminal.rows)
    }

		const onDataDisposable = terminal.onData((data) => {
			const handler = optionsRef.current.onInput
			if (handler) {
				handler(encoder.encode(data))
			}
		})

		const handleResize = () => {
			if (resizeFrame !== 0) {
				return
			}
			resizeFrame = window.requestAnimationFrame(() => {
				resizeFrame = 0
				fitAddon.fit()
				if (terminal.cols === lastNotifiedCols && terminal.rows === lastNotifiedRows) {
					return
				}
				lastNotifiedCols = terminal.cols
				lastNotifiedRows = terminal.rows
				const handler = optionsRef.current.onResize
				if (handler) {
					handler(terminal.cols, terminal.rows)
				}
			})
		}

    const observer = new ResizeObserver(handleResize)
    observer.observe(containerRef.current)

		return () => {
			if (flushFrameRef.current !== 0) {
				window.cancelAnimationFrame(flushFrameRef.current)
				flushFrameRef.current = 0
			}
			writeQueueRef.current = []
			if (resizeFrame !== 0) {
				window.cancelAnimationFrame(resizeFrame)
			}
			observer.disconnect()
			onDataDisposable.dispose()
			terminal.dispose()
      terminalRef.current = null
    }
	}, [encoder, options.fontSize, options.readOnly])

  useEffect(() => {
    if (!terminalRef.current || !options.backgroundColor) return
    terminalRef.current.options.theme = {
      ...terminalRef.current.options.theme,
      background: options.backgroundColor
    }
  }, [options.backgroundColor])

	const flushWrites = useCallback(() => {
		flushFrameRef.current = 0
		if (!terminalRef.current || writeQueueRef.current.length === 0) {
			return
		}

		const queue = writeQueueRef.current
		writeQueueRef.current = []

		let totalLength = 0
		for (const chunk of queue) {
			totalLength += chunk.length
		}

		const merged = new Uint8Array(totalLength)
		let offset = 0
		for (const chunk of queue) {
			merged.set(chunk, offset)
			offset += chunk.length
		}

		const decoded = decoderRef.current.decode(merged, { stream: true })
		if (decoded.length > 0) {
			terminalRef.current.write(decoded)
		}
	}, [])

	const write = useCallback(
		(data: Uint8Array) => {
			if (!terminalRef.current) {
				return
			}
			writeQueueRef.current.push(data)
			if (flushFrameRef.current === 0) {
				flushFrameRef.current = window.requestAnimationFrame(flushWrites)
			}
		},
		[flushWrites]
	)

	const reset = useCallback(() => {
		if (flushFrameRef.current !== 0) {
			window.cancelAnimationFrame(flushFrameRef.current)
			flushFrameRef.current = 0
		}
		writeQueueRef.current = []
		decoderRef.current = new TextDecoder()
		terminalRef.current?.reset()
	}, [])

  return { containerRef, terminalRef, write, reset }
}
