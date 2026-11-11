// Icons come from Hugeicons' free stroke-rounded set (bundled from npm, no external requests),
// keyed by our own short names so call sites stay unchanged.
import { HugeiconsIcon, type IconSvgElement } from '@hugeicons/react'
import {
  Activity01Icon, Add01Icon, AiBrain01Icon, Alert02Icon, Archive02Icon, ArchiveArrowUpIcon, ArrowDown01Icon,
  ArrowExpand01Icon, ArrowLeft01Icon, ArrowRight01Icon, ArrowUp02Icon, Attachment01Icon, BarChartIcon, BulbIcon,
  Calendar03Icon, Cancel01Icon, CheckmarkCircle02Icon, CircleIcon, Clock01Icon, CommandLineIcon, Copy01Icon,
  CpuIcon, DashboardSquare01Icon, DashedLineCircleIcon, Delete02Icon, Dish01Icon, Download01Icon, FavouriteIcon,
  File01Icon, FileDiffIcon, FilterIcon, FingerPrintIcon, FlashIcon, Folder01Icon, Folder02Icon, FolderOpenIcon, GitBranchIcon,
  HierarchySquare02Icon, GitPullRequestIcon, GithubIcon, Globe02Icon, HandIcon, Image01Icon, InboxIcon, Key01Icon, LaptopIcon,
  Layers01Icon, Link01Icon, LinkSquare02Icon, ListViewIcon, Logout03Icon, Message01Icon, Comment01Icon,
  Moon02Icon, MoreHorizontalIcon, Note01Icon, Notification01Icon, Pan01Icon, PauseIcon, PencilEdit02Icon, PlayIcon,
  RefreshIcon, Rocket01Icon, Search01Icon, ServerIcon, Settings02Icon, Shield01Icon, SidebarRightIcon,
  SparklesIcon, StopIcon, Sun03Icon, Target02Icon, TextAlignLeftIcon, Tick02Icon, UserIcon, UserMultipleIcon,
  ViewIcon,
} from '@hugeicons/core-free-icons'

const icons: Record<string, IconSvgElement> = {
  grid: DashboardSquare01Icon,
  system: HierarchySquare02Icon,
  folders: Folder02Icon,
  folder: Folder01Icon,
  inbox: InboxIcon,
  settings: Settings02Icon,
  chat: Message01Icon,
  note: Note01Icon,
  meeting: UserMultipleIcon,
  play: PlayIcon,
  pause: PauseIcon,
  hand: HandIcon,
  clock: Clock01Icon,
  check: Tick02Icon,
  'check-circle': CheckmarkCircle02Icon,
  alert: Alert02Icon,
  x: Cancel01Icon,
  circle: CircleIcon,
  'circle-dashed': DashedLineCircleIcon,
  eye: ViewIcon,
  rocket: Rocket01Icon,
  heart: FavouriteIcon,
  branch: GitBranchIcon,
  pr: GitPullRequestIcon,
  laptop: LaptopIcon,
  server: ServerIcon,
  plus: Add01Icon,
  back: ArrowLeft01Icon,
  forward: ArrowRight01Icon,
  send: ArrowUp02Icon,
  chevron: ArrowDown01Icon,
  panel: SidebarRightIcon,
  message: Comment01Icon,
  stop: StopIcon,
  refresh: RefreshIcon,
  link: Link01Icon,
  external: LinkSquare02Icon,
  user: UserIcon,
  users: UserMultipleIcon,
  bulb: BulbIcon,
  brain: AiBrain01Icon,
  chart: BarChartIcon,
  bell: Notification01Icon,
  calendar: Calendar03Icon,
  logout: Logout03Icon,
  wok: Pan01Icon,
  cloche: Dish01Icon,
  terminal: CommandLineIcon,
  bolt: FlashIcon,
  sparkles: SparklesIcon,
  more: MoreHorizontalIcon,
  edit: PencilEdit02Icon,
  trash: Delete02Icon,
  copy: Copy01Icon,
  paperclip: Attachment01Icon,
  image: Image01Icon,
  file: File01Icon,
  diff: FileDiffIcon,
  log: TextAlignLeftIcon,
  github: GithubIcon,
  globe: Globe02Icon,
  filter: FilterIcon,
  archive: Archive02Icon,
  unarchive: ArchiveArrowUpIcon,
  'folder-open': FolderOpenIcon,
  search: Search01Icon,
  layers: Layers01Icon,
  list: ListViewIcon,
  target: Target02Icon,
  shield: Shield01Icon,
  key: Key01Icon,
  fingerprint: FingerPrintIcon,
  download: Download01Icon,
  pulse: Activity01Icon,
  sun: Sun03Icon,
  moon: Moon02Icon,
  expand: ArrowExpand01Icon,
  cpu: CpuIcon,
}

// Icons snap to one scale so neighbours look alike; thin strokes at small sizes.
const scale = [12, 14, 16, 18, 20, 24, 28]
const snap = (n: number) => scale.reduce((a, b) => (Math.abs(b - n) < Math.abs(a - n) ? b : a))

export default function Icon({ name, size = 18, label = '', className = '' }: {
  name: string
  size?: number
  label?: string
  stroke?: number
  className?: string
}) {
  size = size > 28 ? size : snap(size)
  const a11y = {
    role: label ? 'img' : undefined,
    'aria-label': label || undefined,
    'aria-hidden': label ? undefined : true,
    className: `flex-none block ${className}`,
  }
  const icon = icons[name]
  if (!icon) {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" {...a11y}>
        <circle cx="12" cy="12" r="2" />
      </svg>
    )
  }
  const stroke = size <= 14 ? 2 : size >= 20 ? 1.6 : 1.8
  return <HugeiconsIcon icon={icon} size={size} strokeWidth={stroke} color="currentColor" {...a11y} />
}
