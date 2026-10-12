import { CheckCircle2, Circle } from 'lucide-react'
import { motion } from 'motion/react'
import { checkPassword, ruleText, rules, type PasswordPolicy } from './policy'

type T = (k: string, v?: Record<string, string | number>) => string

export function PolicyChecklist({
  policy,
  password,
  username,
  confirm,
  t,
}: {
  policy: PasswordPolicy
  password: string
  username: string
  confirm?: string
  t: T
}) {
  const failed = new Set(checkPassword(password, username, policy))
  const items = rules(policy).map((r) => ({ key: r, text: ruleText(t, r, policy), ok: password !== '' && !failed.has(r) }))
  if (confirm !== undefined) items.push({ key: 'username', text: t('pp.match'), ok: password !== '' && password === confirm })
  return (
    <ul className="checklist">
      {items.map((i, n) => (
        <li key={`${i.key}-${n}`} className={i.ok ? 'ok' : ''}>
          {i.ok ? (
            <motion.span className="pop-icon" initial={{ scale: 0.3 }} animate={{ scale: 1 }} transition={{ type: 'spring', stiffness: 520, damping: 18 }}>
              <CheckCircle2 size={16} />
            </motion.span>
          ) : (
            <Circle size={16} />
          )}
          {i.text}
        </li>
      ))}
    </ul>
  )
}
