// Display-only clock math for row timestamps (card web-2 slice 2). Parsing a
// timestamp here is safe because the result is only rendered; the write path
// keeps timestamps as unparsed strings (FRONTEND-DESIGN.md 5).

export function relativeTime(iso: string, now: Date = new Date()): string {
  const then = new Date(iso)
  if (Number.isNaN(then.getTime())) return iso
  const secs = Math.floor((now.getTime() - then.getTime()) / 1000)
  if (secs < 45) return 'just now'
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d ago`
  return iso.slice(0, 10)
}
