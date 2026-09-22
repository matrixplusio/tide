import type { DotState, Tone } from '../ui'

export function phaseTone(phase: string): Tone {
  return phase === 'Succeeded' ? 'green' : phase === 'Running' ? 'orange' : phase ? 'red' : 'neutral'
}

export function phaseDot(phase: string): DotState {
  return phase === 'Succeeded' ? 'ok' : phase === 'Running' || !phase ? 'run' : 'bad'
}
