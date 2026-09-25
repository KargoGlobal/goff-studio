import { Fragment } from 'react'
import { type Token, tokensToText } from '@/lib/tokens'
import { cn } from '@/lib/cn'

function chip(token: Token, key: number) {
  switch (token.kind) {
    case 'attribute':
      return (
        <code
          key={key}
          className="rounded bg-canvas px-1.5 py-0.5 font-mono text-[12.5px] text-ink"
        >
          {token.text}
        </code>
      )
    case 'value':
      return (
        <code
          key={key}
          className="rounded bg-brand-soft px-1.5 py-0.5 font-mono text-[12.5px] text-brand"
        >
          {token.text}
        </code>
      )
    case 'operator':
      return (
        <span key={key} className="text-ink-soft">
          {token.text}
        </span>
      )
    case 'combinator':
      return (
        <span key={key} className="font-semibold uppercase text-[11px] tracking-wide text-ink-muted">
          {token.text}
        </span>
      )
    case 'negation':
      return (
        <span key={key} className="font-semibold uppercase text-[11px] tracking-wide text-warn">
          {token.text}
        </span>
      )
    case 'open':
      return (
        <span key={key} className="text-ink-muted">
          (
        </span>
      )
    case 'close':
      return (
        <span key={key} className="text-ink-muted">
          )
        </span>
      )
  }
}

export function ConditionView({
  tokens,
  prefix = 'If',
  empty = 'anyone',
  className,
}: {
  tokens: Token[]
  prefix?: string
  empty?: string
  className?: string
}) {
  if (tokens.length === 0) {
    return <span className={cn('text-[13px] text-ink-soft', className)}>{empty}</span>
  }

  return (
    <span
      title={tokensToText(tokens)}
      className={cn('flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[13px]', className)}
    >
      {prefix && <span className="text-ink-soft">{prefix} </span>}
      {tokens.map((token, i) => (
        <Fragment key={i}>
          {chip(token, i)}{' '}
        </Fragment>
      ))}
    </span>
  )
}
