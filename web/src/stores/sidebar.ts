export interface ClickableRegion {
  start: number
  end: number
  start_col: number
  end_col: number
  action: string
  target: string
}

export interface RenderPayload {
  seq: number
  content: string
  pinned_content: string
  width: number
  height: number
  total_lines: number
  pinned_height: number
  viewport_offset: number
  regions: ClickableRegion[]
  pinned_regions: ClickableRegion[]
  is_touch_mode: boolean
  sidebar_bg?: string
  terminal_bg?: string
}

export interface SidebarState {
  render?: RenderPayload
}
