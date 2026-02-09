import { useEffect, useMemo } from 'react'
import { useTerminal } from '../../hooks/useTerminal'
import type { ClickableRegion } from '../../stores/sidebar'
import { getCharacterCoords } from '../../utils/terminal'

interface SidebarTerminalProps {
  content: string
  regions: ClickableRegion[]
  onRegionClick: (region: ClickableRegion) => void
  onResize: (cols: number, rows: number) => void
}

export default function SidebarTerminal({ content, regions, onRegionClick, onResize }: SidebarTerminalProps) {
  const { containerRef, terminalRef, write, reset } = useTerminal({ readOnly: true, fontSize: 14, onResize })

  useEffect(() => {
    reset()
    if (content) {
      write(new TextEncoder().encode(content))
    }
  }, [content, reset, write])

  const regionLookup = useMemo(() => regions ?? [], [regions])

  useEffect(() => {
    const element = containerRef.current
    if (!element) {
      return
    }

    const handleClick = (event: MouseEvent) => {
      const terminal = terminalRef.current
      if (!terminal) {
        return
      }
      const coords = getCharacterCoords(terminal, event)
      if (!coords) {
        return
      }
      const [x, y] = coords
      const match = regionLookup.find((region) => {
        const endCol = region.end_col === 0 ? terminal.cols : region.end_col
        return y >= region.start && y <= region.end && x >= region.start_col && x <= endCol
      })
      if (match) {
        onRegionClick(match)
      }
    }

    element.addEventListener('click', handleClick)
    return () => element.removeEventListener('click', handleClick)
  }, [containerRef, onRegionClick, regionLookup, terminalRef])

  return <div className="sidebar-terminal" ref={containerRef} />
}
