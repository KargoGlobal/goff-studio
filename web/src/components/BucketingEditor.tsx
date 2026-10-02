import { useState } from 'react'
import type { Flag } from '@/lib/api'
import { Button, Code } from '@/components/ui/primitives'
import { identifierInputProps } from '@/lib/inputProps'
import { hasSplit, rulesSplittingOneValue } from '@/lib/bucketing'

export function BucketingEditor({
  flag,
  attributes,
  disabled,
  onSave,
}: {
  flag: Pick<Flag, 'bucketingKey' | 'default' | 'rules'>
  attributes: string[]
  disabled?: boolean
  onSave: (attribute: string) => void
}) {
  const current = flag.bucketingKey ?? ''
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(current)
  const pinned = rulesSplittingOneValue(flag, current)

  return (
    <details open={Boolean(current) || undefined} className="mt-4 border-t pt-4">
      <summary className="cursor-pointer text-sm font-semibold text-ink-soft hover:text-ink">Advanced</summary>
      <div className="mt-2.5 space-y-2">
        <h3 className="text-[13px] font-semibold text-ink-soft">Split by</h3>
        <p className="text-[13px] text-ink-muted">
          Leave empty to split each evaluation. Set an attribute, like <Code>accountId</Code>, to keep every
          request with the same value on the same side of a percentage split.
        </p>

        {editing ? (
          <div className="flex items-center gap-2 max-md:flex-wrap">
            <input
              autoFocus
              type="text"
              list="studio-bucketing-attributes"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setEditing(false)
              }}
              placeholder="evaluation (default)"
              aria-label="Split by attribute"
              {...identifierInputProps}
              className="h-8 w-56 rounded-md border bg-surface px-2 font-mono text-[12.5px] text-ink focus:border-brand focus:outline-none max-md:h-11 max-md:w-full"
            />
            <datalist id="studio-bucketing-attributes">
              {attributes.map((a) => (
                <option key={a} value={a} />
              ))}
            </datalist>
            <Button
              size="sm"
              disabled={draft.trim() === current}
              onClick={() => {
                onSave(draft.trim())
                setEditing(false)
              }}
            >
              Review change
            </Button>
            <Button size="sm" variant="outline" onClick={() => setEditing(false)}>
              Cancel
            </Button>
          </div>
        ) : (
          <div className="flex items-center gap-2 max-md:flex-wrap">
            <p className="text-[13px] text-ink">
              {current ? (
                <>
                  Each <Code>{current}</Code> stays on one side
                </>
              ) : (
                'Each evaluation is split on its own'
              )}
            </p>
            {!disabled && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setDraft(current)
                  setEditing(true)
                }}
              >
                Change
              </Button>
            )}
          </div>
        )}

        {current && (
          <p className="text-[12.5px] text-warn">
            Requests without <Code>{current}</Code> skip every rule and get the SDK default.
          </p>
        )}
        {current && !hasSplit(flag) && (
          <p className="text-[12.5px] text-warn">
            This flag has no percentage split, so this setting has no effect.
          </p>
        )}
        {pinned.map((r) => (
          <p key={r.name} className="text-[12.5px] text-warn">
            {r.name || 'A rule'} targets one <Code>{current}</Code>, so its split always lands on one side.
          </p>
        ))}
      </div>
    </details>
  )
}
