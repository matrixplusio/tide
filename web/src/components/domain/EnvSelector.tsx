import { useTranslation } from 'react-i18next'
import type { Ref } from 'react'
import { useMe } from '../../app/session'
import { TIERS, tierLabel } from '../../lib/permissions'
import { CheckboxGroup } from '../ui'

export interface EnvSelectorProps {
  value: string[]
  onChange: (v: string[]) => void
  onBlur?: () => void
  id?: string
  'aria-invalid'?: boolean
  'aria-describedby'?: string
  ref?: Ref<HTMLFieldSetElement>
}

/**
 * Environment scope: everything (`*`), by tier (`tier:<Tier>`) or specific
 * environments. `*` stands alone, so picking it clears and locks the rest.
 */
export function EnvSelector({ value, onChange, onBlur, id, ref, ...aria }: EnvSelectorProps) {
  const { t } = useTranslation()
  const me = useMe()
  const all = value.includes('*')
  const envs = me.environments ?? []
  const known = new Set(['*', ...TIERS.map(([t]) => `tier:${t}`), ...envs.map((e) => e.name)])
  // Keep selectors that no longer exist visible, so they can be removed.
  const unknown = value.filter((v) => !known.has(v))
  const set = (part: string[], group: string[]) => onChange([...value.filter((v) => !group.includes(v)), ...part])
  const tierValues = TIERS.map(([t]) => `tier:${t}`)
  const envValues = [...envs.map((e) => e.name), ...unknown]

  return (
    <div className="fcontrol" style={{ gap: 2 }}>
      <CheckboxGroup
        ref={ref}
        id={id}
        {...aria}
        onBlur={onBlur}
        options={[{ value: '*', label: t('domain.allEnvs') }]}
        value={all ? ['*'] : []}
        onChange={(v) => onChange(v.includes('*') ? ['*'] : [])}
      />
      <CheckboxGroup
        legend={t('domain.byTier')}
        options={TIERS.map(([tier]) => ({ value: `tier:${tier}`, label: t('scope.tierEnvs', { tier: tierLabel(tier) }), disabled: all }))}
        value={value.filter((v) => tierValues.includes(v))}
        onChange={(v) => set(v, tierValues)}
        onBlur={onBlur}
      />
      {envValues.length > 0 && (
        <CheckboxGroup
          legend={t('domain.specificEnvs')}
          options={envValues.map((name) => {
            const e = envs.find((x) => x.name === name)
            return { value: name, label: e ? (e.displayName && e.displayName !== name ? t('scope.envNamed', { display: e.displayName, name }) : name) : t('domain.envGone', { name }), disabled: all && !unknown.includes(name) }
          })}
          value={value.filter((v) => envValues.includes(v))}
          onChange={(v) => set(v, envValues)}
          onBlur={onBlur}
        />
      )}
    </div>
  )
}
