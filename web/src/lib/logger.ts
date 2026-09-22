// The only module allowed to touch console.*. Never pass passwords, tokens,
// cookies or request bodies that may contain them.
type Level = 'debug' | 'info' | 'warn' | 'error'

const order: Record<Level, number> = { debug: 10, info: 20, warn: 30, error: 40 }
const threshold: Level = import.meta.env.DEV ? 'debug' : 'warn'

function emit(level: Level, msg: string, fields?: Record<string, unknown>) {
  if (order[level] < order[threshold]) return
  const entry = { time: new Date().toISOString(), level, msg, ...fields }
  switch (level) {
    case 'debug':
      console.debug(entry)
      break
    case 'info':
      console.info(entry)
      break
    case 'warn':
      console.warn(entry)
      break
    case 'error':
      console.error(entry)
      break
  }
}

export const logger = {
  debug: (msg: string, fields?: Record<string, unknown>) => emit('debug', msg, fields),
  info: (msg: string, fields?: Record<string, unknown>) => emit('info', msg, fields),
  warn: (msg: string, fields?: Record<string, unknown>) => emit('warn', msg, fields),
  error: (msg: string, fields?: Record<string, unknown>) => emit('error', msg, fields),
}
