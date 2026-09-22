import { useTranslation } from 'react-i18next'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { fmtTime } from '../../../lib/format'
import { applyServerError } from '../../../lib/forms'
import {
  Button,
  ButtonRow,
  ConfirmModal,
  EmptyState,
  ErrorState,
  Form,
  FormErrorBanner,
  FormField,
  Group,
  GroupHeader,
  Input,
  Loading,
  Modal,
  Note,
  Page,
  Pill,
  Row,
  Select,
  useToast,
} from '../../../components/ui'
import { AdminToolbar } from '../AdminToolbar'
import { useCISnippet, useCITokens, useCreateCIToken, useRevokeCIToken, useSettings } from '../queries'
import { ciTokenSchema, type CITokenValues } from '../schemas'
import type { CIToken } from '../types'
import { IntakeList } from './IntakeList'

/** CI-triggered releases: the tokens pipelines authenticate with, the step to
 *  paste into a pipeline, and what Tide did with what it has been told. */
export function CIPage() {
  const { t } = useTranslation()
  const [creating, setCreating] = useState(false)
  return (
    <>
      <AdminToolbar title={t('ci.title')} sub={t('ci.sub')}>
        <Button size="small" onClick={() => setCreating(true)}>
          {t('ci.newToken')}
        </Button>
      </AdminToolbar>
      <Page>
        <Note style={{ paddingTop: 0, marginBottom: 8 }}>{t('ci.note')}</Note>
        <TokenList />
        <SnippetSection />
        <GroupHeader>{t('ci.intakes')}</GroupHeader>
        <IntakeList />
      </Page>
      {creating && <CreateTokenModal onClose={() => setCreating(false)} />}
    </>
  )
}

function TokenList() {
  const { t } = useTranslation()
  const tokens = useCITokens()
  const [revoking, setRevoking] = useState<CIToken | null>(null)
  const list = tokens.data?.items ?? []
  return (
    <>
      <GroupHeader>{t('ci.tokens')}</GroupHeader>
      {tokens.isPending && <Loading />}
      {tokens.error && <ErrorState error={tokens.error} onRetry={() => void tokens.refetch()} />}
      {tokens.data && (
        <Group>
          {list.length === 0 && <EmptyState>{t('ci.noTokens')}</EmptyState>}
          {list.map((k) => (
            <Row key={k.id}>
              <div className="grow">
                <div className="t ellipsis">
                  {k.name} <span className="mono muted">{k.prefix}…</span>
                </div>
                <div className="d ellipsis">
                  {t('ci.createdBy', { name: k.createdByName || k.createdBy, at: fmtTime(k.createdAt) })}
                  {' · '}
                  {k.lastUsedAt ? t('ci.lastUsed', { at: fmtTime(k.lastUsedAt) }) : t('ci.neverUsed')}
                </div>
              </div>
              {k.revokedAt ? (
                <Pill>{t('ci.revoked')}</Pill>
              ) : (
                <Button size="small" variant="danger" onClick={() => setRevoking(k)}>
                  {t('ci.revoke')}
                </Button>
              )}
            </Row>
          ))}
        </Group>
      )}
      {revoking && <RevokeTokenModal token={revoking} onClose={() => setRevoking(null)} />}
    </>
  )
}

function RevokeTokenModal({ token, onClose }: { token: CIToken; onClose: () => void }) {
  const { t } = useTranslation()
  const toast = useToast()
  const revoke = useRevokeCIToken()
  const [error, setError] = useState<unknown>(null)
  return (
    <ConfirmModal
      title={t('ci.revokeTitle', { name: token.name })}
      confirmLabel={t('ci.revoke')}
      danger
      pending={revoke.isPending}
      error={error}
      onClose={onClose}
      onConfirm={async () => {
        setError(null)
        try {
          await revoke.mutateAsync(token.id)
          toast.success(t('ci.revoked'))
          onClose()
        } catch (e) {
          setError(e)
        }
      }}
    >
      {t('ci.revokeWarning')}
    </ConfirmModal>
  )
}

function CreateTokenModal({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const create = useCreateCIToken()
  const [secret, setSecret] = useState('')
  const [formError, setFormError] = useState<unknown>(null)
  const form = useForm<CITokenValues>({ resolver: zodResolver(ciTokenSchema), mode: 'onTouched', defaultValues: { name: '' } })
  const { register, handleSubmit, formState } = form

  const onSubmit = handleSubmit(async (v) => {
    setFormError(null)
    try {
      const r = await create.mutateAsync({ name: v.name.trim() })
      setSecret(r?.secret ?? '')
    } catch (e) {
      setFormError(applyServerError(form, e))
    }
  }, () => setFormError(null))

  // Tide stores a hash, so this is the only time the token can be read.
  if (secret) {
    return (
      <Modal title={t('ci.tokenCreated')} subtitle={t('ci.tokenOnce')} onClose={onClose}>
        <Group>
          <Row>
            <code className="mono grow" style={{ wordBreak: 'break-all' }}>
              {secret}
            </code>
          </Row>
        </Group>
        <Note>{t('ci.tokenWhere')}</Note>
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button onClick={onClose} data-autofocus>
            {t('ci.tokenSaved')}
          </Button>
        </ButtonRow>
      </Modal>
    )
  }
  return (
    <Modal title={t('ci.newToken')} subtitle={t('ci.newTokenSub')} onClose={onClose} closeOnEsc={!formState.isSubmitting}>
      <Form onSubmit={onSubmit}>
        <Group form>
          <FormField label={t('ci.tokenName')} error={formState.errors.name?.message} required hint={t('ci.tokenNameHint')}>
            {(p) => <Input {...p} {...register('name')} autoComplete="off" placeholder="acme-ci" data-autofocus />}
          </FormField>
        </Group>
        <FormErrorBanner error={formError} />
        <ButtonRow style={{ marginTop: 16 }}>
          <span className="grow" />
          <Button variant="quiet" onClick={onClose} disabled={formState.isSubmitting}>
            {t('admin.cancel')}
          </Button>
          <Button type="submit" loading={formState.isSubmitting} loadingText={t('admin.creating')}>
            {t('admin.create')}
          </Button>
        </ButtonRow>
      </Form>
    </Modal>
  )
}

function SnippetSection() {
  const { t } = useTranslation()
  const toast = useToast()
  const settings = useSettings()
  const envs = settings.data?.environments?.items ?? []
  const [env, setEnv] = useState('')
  const chosen = env || envs[0]?.name || ''
  const snippet = useCISnippet(chosen)

  const copy = async () => {
    const text = snippet.data?.snippet
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('ci.copied'))
    } catch {
      // Clipboard access can be refused (insecure context, denied permission);
      // the snippet is on screen and can still be selected by hand.
      toast.error(t('ci.copyFailed'))
    }
  }

  return (
    <>
      <GroupHeader
        right={
          <span className="inline-control">
            <Select
              appearance="filled"
              aria-label={t('ci.forEnv')}
              options={envs.map((e) => [e.name, e.displayName || e.name] as const)}
              value={chosen}
              onChange={(e) => setEnv(e.target.value)}
            />
            <Button size="small" variant="quiet" disabled={!snippet.data} onClick={() => void copy()}>
              {t('ci.copy')}
            </Button>
          </span>
        }
      >
        {t('ci.snippet')}
      </GroupHeader>
      {envs.length === 0 && (
        <Group>
          <EmptyState>{t('ci.noEnvs')}</EmptyState>
        </Group>
      )}
      {snippet.isPending && chosen !== '' && <Loading />}
      {snippet.error && <ErrorState error={snippet.error} onRetry={() => void snippet.refetch()} />}
      {snippet.data && (
        <>
          <Group>
            <Row>
              <pre className="mono grow" style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
                {snippet.data.snippet}
              </pre>
            </Row>
          </Group>
          <Note>{snippet.data.mode === 'off' ? t('ci.snippetOff', { env: snippet.data.env }) : t('ci.snippetNote')}</Note>
        </>
      )}
    </>
  )
}
