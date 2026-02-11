import type { Terminal } from 'xterm'

type XtermCore = {
  _renderService?: {
    dimensions?: {
      css?: {
        cell?: {
          width: number
          height: number
        }
      }
    }
  }
}

export function getCharacterCoords(terminal: Terminal, event: MouseEvent): [number, number] | null {
  const element = terminal.element
  if (!element) {
    return null
  }

  const rect = element.getBoundingClientRect()
  const core = (terminal as unknown as { _core?: XtermCore })._core
  const cell = core?._renderService?.dimensions?.css?.cell
  if (!cell) {
    return null
  }

  const x = Math.floor((event.clientX - rect.left) / cell.width)
  const y = Math.floor((event.clientY - rect.top) / cell.height)

  if (x < 0 || y < 0 || x >= terminal.cols || y >= terminal.rows) {
    return null
  }

  return [x, y]
}
