import { useEffect, useState } from 'react'

export type AvatarUser = {
  id: string
  name: string
  username: string
  first_name?: string
  last_name?: string
  has_avatar?: boolean
  avatar_version?: string
  gravatar?: string
}

function initials(u: AvatarUser) {
  const parts = [u.first_name, u.last_name].filter(Boolean) as string[]
  const src = parts.length ? parts : (u.name || u.username).split(/\s+/)
  return src
    .slice(0, 2)
    .map((p) => p[0]?.toUpperCase() ?? '')
    .join('')
}

function hue(s: string) {
  let h = 0
  for (const ch of s) h = (h * 31 + ch.codePointAt(0)!) % 360
  return h
}

export function Avatar({ user, size = 32 }: { user: AvatarUser; size?: number }) {
  const sources = [
    user.has_avatar ? `/api/users/${encodeURIComponent(user.id)}/avatar?v=${user.avatar_version ?? 0}` : '',
    user.gravatar ? `https://gravatar.com/avatar/${user.gravatar}?s=${size * 2}&d=identicon` : '',
  ].filter(Boolean)
  const [index, setIndex] = useState(0)
  useEffect(() => setIndex(0), [sources.join('|')])
  const src = sources[index]
  if (!src) {
    return (
      <span
        className="avatar avatar-initials"
        style={{ width: size, height: size, fontSize: size * 0.4, background: `hsl(${hue(user.username)} 55% 45%)` }}
        aria-hidden
      >
        {initials(user)}
      </span>
    )
  }
  return <img className="avatar" src={src} width={size} height={size} alt="" referrerPolicy="no-referrer" onError={() => setIndex((i) => i + 1)} />
}
