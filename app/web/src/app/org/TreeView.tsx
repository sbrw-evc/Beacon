import { useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { AnimatePresence, motion } from 'motion/react'
import { useT } from '../../i18n'
import { orgStrings } from './strings'
import type { TreeIndex, TreeItem } from './treeIndex'
import './org.css'

type Props<T extends TreeItem> = {
  index: TreeIndex<T>
  selected?: string
  onSelect: (id: string) => void
  visible?: Set<string>
  label: (item: T) => ReactNode
  meta?: (item: T) => ReactNode
  empty: string
}

export function Tree<T extends TreeItem>(props: Props<T>) {
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set())
  const toggle = (id: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  const roots = props.index.roots().filter((r) => !props.visible || props.visible.has(r.id))
  if (roots.length === 0) return <p className="muted org-tree-empty">{props.empty}</p>
  return (
    <ul className="org-tree" role="tree">
      {roots.map((item) => (
        <TreeNode key={item.id} item={item} depth={0} collapsed={collapsed} toggle={toggle} {...props} />
      ))}
    </ul>
  )
}

function TreeNode<T extends TreeItem>({
  item,
  depth,
  collapsed,
  toggle,
  ...props
}: Props<T> & { item: T; depth: number; collapsed: Set<string>; toggle: (id: string) => void }) {
  const t = useT(orgStrings)
  const kids = props.index.children(item.id).filter((c) => !props.visible || props.visible.has(c.id))
  const open = kids.length > 0 && (props.visible !== undefined || !collapsed.has(item.id))
  const active = props.selected === item.id
  return (
    <li role="treeitem" aria-expanded={kids.length ? open : undefined} aria-selected={active}>
      <div className={`org-tree-row${active ? ' active' : ''}`} style={{ paddingLeft: 6 + depth * 18 }}>
        {kids.length > 0 ? (
          <button type="button" className="org-tree-toggle" onClick={() => toggle(item.id)} aria-label={t(open ? 'org.collapse' : 'org.expand')}>
            <motion.span animate={{ rotate: open ? 90 : 0 }} transition={{ duration: 0.18 }} className="org-tree-chevron">
              <ChevronRight size={15} />
            </motion.span>
          </button>
        ) : (
          <span className="org-tree-toggle" aria-hidden />
        )}
        <button type="button" className="org-tree-label" onClick={() => props.onSelect(item.id)}>
          <span className="org-tree-name">{props.label(item)}</span>
          {props.meta && <span className="org-tree-meta">{props.meta(item)}</span>}
        </button>
      </div>
      <AnimatePresence initial={false}>
        {open && (
          <motion.ul
            role="group"
            className="org-tree-group"
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.22, ease: [0.22, 1, 0.36, 1] }}
          >
            {kids.map((k) => (
              <TreeNode key={k.id} item={k} depth={depth + 1} collapsed={collapsed} toggle={toggle} {...props} />
            ))}
          </motion.ul>
        )}
      </AnimatePresence>
    </li>
  )
}
