import type { ReactNode } from 'react'

interface SidebarDrawerProps {
  open: boolean
  onClose: () => void
  children: ReactNode
}

export default function SidebarDrawer({ open, onClose, children }: SidebarDrawerProps) {
  return (
    <>
      <div className={`drawer-backdrop ${open ? 'open' : ''}`} onClick={onClose} />
      <aside className={`drawer ${open ? 'open' : ''}`}>
        <div className="drawer-header">Sidebar</div>
        {children}
      </aside>
    </>
  )
}
