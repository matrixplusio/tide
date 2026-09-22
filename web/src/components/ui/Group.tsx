import type { CSSProperties, ReactNode } from 'react'
import { Link } from 'react-router-dom'

export function Group({ children, form, className, style }: { children: ReactNode; form?: boolean; className?: string; style?: CSSProperties }) {
  return (
    <div className={['group', form && 'form', className].filter(Boolean).join(' ')} style={style}>
      {children}
    </div>
  )
}

export function GroupHeader({ children, right, id }: { children: ReactNode; right?: ReactNode; id?: string }) {
  return (
    <div className="ghead" id={id}>
      {children}
      {right !== undefined && <span className="r">{right}</span>}
    </div>
  )
}

export function Row({ children, to, href, onClick, className, title, style }: { children: ReactNode; to?: string; href?: string; onClick?: () => void; className?: string; title?: string; style?: CSSProperties }) {
  const c = ['row', className].filter(Boolean).join(' ')
  if (to) return <Link className={c} to={to} title={title} style={style}>{children}</Link>
  if (href) return <a className={c} href={href} target="_blank" rel="noreferrer" title={title} style={style}>{children}</a>
  if (onClick)
    return (
      <button type="button" className={`${c} tap rowbtn`} onClick={onClick} title={title} style={style}>
        {children}
      </button>
    )
  return <div className={c} title={title} style={style}>{children}</div>
}

export function RowText({ title, detail, mono }: { title: ReactNode; detail?: ReactNode; mono?: boolean }) {
  return (
    <div className="grow">
      <div className={`t ${mono ? 'mono ellipsis' : ''}`}>{title}</div>
      {detail !== undefined && detail !== null && detail !== '' && <div className="d">{detail}</div>}
    </div>
  )
}

export function KV({ k, children, mono }: { k: ReactNode; children: ReactNode; mono?: boolean }) {
  return (
    <div className="row kv">
      <div className="t muted kv-k">{k}</div>
      <div className={`grow v ${mono ? 'mono break' : ''}`}>{children}</div>
    </div>
  )
}

export function Note({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return <div className="note" style={style}>{children}</div>
}

export function Chev() {
  return (
    <svg className="chev" width="7" height="12" viewBox="0 0 8 13" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
      <path d="M1.5 1.5L6.5 6.5L1.5 11.5" />
    </svg>
  )
}

export function ButtonRow({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return <div className="btnrow" style={style}>{children}</div>
}
