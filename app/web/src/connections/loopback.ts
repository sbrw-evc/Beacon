const LOOPBACK = /^(localhost|127(\.\d+){3}|\[?::1\]?|0\.0\.0\.0)$/i

export function isLoopbackHost(host: string) {
  return LOOPBACK.test(host.trim())
}

export function isLoopbackUrl(addr: string) {
  try {
    return isLoopbackHost(new URL(addr.trim()).hostname)
  } catch {
    return false
  }
}
