import { useState } from 'react'
import { Check, ChevronDown, ChevronRight, Copy } from 'lucide-react'
import { cn } from '@/lib/cn'
import { prettyJson, summarizeJson } from '@/lib/jsonValue'

// Renders as siblings so a flex-wrap parent can put the expanded block on its own full-width line.
export function JsonValue({
  value,
  label = 'value',
  className,
  codeClassName,
}: {
  value: unknown
  label?: string
  className?: string
  codeClassName?: string
}) {
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const { inline, expandable, size } = summarizeJson(value)

  const summary = (
    <div className={cn('flex min-w-0 items-center gap-1.5', className)}>
      <code
        title={expandable ? inline : undefined}
        className={cn(
          'min-w-0 truncate rounded bg-canvas px-1.5 py-0.5 font-mono text-[12.5px] text-ink-soft',
          codeClassName,
        )}
      >
        {inline}
      </code>
      {expandable && (
        <button
          type="button"
          aria-expanded={open}
          aria-label={`${open ? 'Hide' : 'Show'} the full ${label}`}
          onClick={() => setOpen((o) => !o)}
          className="inline-flex shrink-0 items-center gap-0.5 whitespace-nowrap rounded px-1 text-xs font-medium opacity-80 hover:opacity-100 max-md:min-h-11"
        >
          {size}
          {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        </button>
      )}
    </div>
  )
  if (!expandable || !open) return summary

  const copy = async () => {
    await navigator.clipboard.writeText(prettyJson(value))
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <>
      {summary}
      <div className="relative min-w-0 basis-full">
        <pre className="max-h-72 overflow-auto rounded-md bg-canvas p-3 pr-10 text-left font-mono text-[12.5px] text-ink-soft dark:bg-line dark:text-white">
          {prettyJson(value)}
        </pre>
        <button
          type="button"
          aria-label={`Copy the ${label}`}
          onClick={() => void copy()}
          className="absolute right-2 top-2 rounded p-1 text-ink-muted hover:bg-surface hover:text-ink"
        >
          {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
        </button>
      </div>
    </>
  )
}
