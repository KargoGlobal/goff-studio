import { useMemo } from 'react'
import { QueryBuilder, type Field, type RuleGroupType } from 'react-querybuilder'
import 'react-querybuilder/dist/query-builder.css'
import { arityOf, VISIBLE_OPERATORS, describeGroup, fieldsFrom, queryFromGroup } from '@/lib/query'
import { ChipInput } from '@/components/ChipInput'
import { Code } from '@/components/ui/primitives'

const operators = VISIBLE_OPERATORS.map((o) => ({ name: o.name, label: o.label, value: o.name }))

export function RuleBuilder({
  value,
  onChange,
  attributes,
  disabled,
}: {
  value: RuleGroupType
  onChange: (next: RuleGroupType) => void
  attributes: string[]
  disabled?: boolean
}) {
  // A blank first field keeps a new condition empty but still renders every control.
  const fields: Field[] = useMemo(() => fieldsFrom(['', ...attributes]), [attributes])
  const description = describeGroup(value)
  const query = queryFromGroup(value)

  return (
    <div className="space-y-3">
      <div className="studio-qb">
        <QueryBuilder
          fields={fields}
          autoSelectField
          operators={operators}
          query={value}
          onQueryChange={onChange}
          disabled={disabled}
          listsAsArrays
          showNotToggle
          controlClassnames={{
            queryBuilder: 'space-y-2',
            ruleGroup: 'rounded-lg border bg-canvas p-2.5 space-y-2',
            header: 'flex items-center gap-2 flex-wrap',
            body: 'space-y-2',
            rule: 'flex items-center gap-2 flex-wrap',
            combinators:
              'h-8 rounded-md border bg-surface px-2 text-[12.5px] text-ink focus:border-brand focus:outline-none',
            fields:
              'h-8 rounded-md border bg-surface px-2 text-[12.5px] text-ink focus:border-brand focus:outline-none',
            operators:
              'h-8 rounded-md border bg-surface px-2 text-[12.5px] text-ink focus:border-brand focus:outline-none',
            value:
              'h-8 min-w-40 rounded-md border bg-surface px-2 font-mono text-[12.5px] text-ink focus:border-brand focus:outline-none',
            addRule:
              'h-8 rounded-md border bg-surface px-2.5 text-[12.5px] font-medium text-ink hover:bg-canvas',
            addGroup:
              'h-8 rounded-md border bg-surface px-2.5 text-[12.5px] font-medium text-ink-soft hover:bg-canvas',
            removeRule:
              'h-8 w-8 rounded-md border bg-surface text-[12.5px] text-ink-muted hover:border-danger hover:text-danger',
            removeGroup:
              'h-8 w-8 rounded-md border bg-surface text-[12.5px] text-ink-muted hover:border-danger hover:text-danger',
          }}
          translations={{
            addRule: { label: '+ Condition' },
            addGroup: { label: '+ Group' },
            removeRule: { label: '×', title: 'Remove condition' },
            removeGroup: { label: '×', title: 'Remove group' },
            fields: { title: 'Attribute' },
            operators: { title: 'Operator' },
            value: { title: 'Value' },
          }}
          controlElements={{
            valueEditor: ValueEditor,
            fieldSelector: AttributeInput,
            notToggle: NotToggle,
          }}
        />
      </div>

      <div className="rounded-lg border bg-surface p-3">
        <p className="text-[11px] font-medium uppercase tracking-wide text-ink-muted">
          Who this matches
        </p>
        <p className="mt-1 text-[13px] text-ink">
          {description ? `Users where ${description}` : 'Nobody yet, add a condition above.'}
        </p>
        {query && (
          <details className="mt-2">
            <summary className="cursor-pointer text-[11.5px] text-ink-muted hover:text-ink">
              Query
            </summary>
            <Code className="mt-1.5 block overflow-x-auto whitespace-pre p-2">{query}</Code>
          </details>
        )}
      </div>
    </div>
  )
}

function NotToggle({ checked, handleOnChange, disabled }: any) {
  return (
    <label
      className={`inline-flex h-8 cursor-pointer select-none items-center gap-1.5 rounded-md border px-2 text-[12.5px] ${
        checked ? 'border-brand bg-brand/10 text-ink' : 'bg-surface text-ink-soft'
      } ${disabled ? 'pointer-events-none opacity-50' : 'hover:bg-canvas'}`}
      title="Match everyone this group does not describe"
    >
      <input
        type="checkbox"
        checked={Boolean(checked)}
        disabled={disabled}
        onChange={(e) => handleOnChange(e.target.checked)}
        className="h-3.5 w-3.5 accent-brand"
      />
      Invert
    </label>
  )
}

function ValueEditor({ value, handleOnChange, operator, disabled }: any) {
  const arity = arityOf(operator)
  if (arity === 0) return null

  const text = value === undefined || value === null ? '' : String(value)

  if (arity === 2) {
    const values = text
      .split(',')
      .map((v: string) => v.trim())
      .filter((v: string) => v !== '')

    return (
      <ChipInput
        label="Value"
        values={values}
        disabled={disabled}
        placeholder="type a value, then Enter"
        onChange={(next) => handleOnChange(next.join(', '))}
      />
    )
  }

  return (
    <input
      type="text"
      value={text}
      disabled={disabled}
      onChange={(e) => handleOnChange(e.target.value)}
      placeholder="value"
      aria-label="Value"
      className="h-8 min-w-40 flex-1 rounded-md border bg-surface px-2 font-mono text-[12.5px] text-ink focus:border-brand focus:outline-none"
    />
  )
}

function AttributeInput({ value, handleOnChange, options, disabled }: any) {
  const listId = 'studio-attributes'

  return (
    <>
      <input
        type="text"
        value={value ?? ''}
        disabled={disabled}
        list={listId}
        onChange={(e) => handleOnChange(e.target.value)}
        placeholder="attribute"
        aria-label="Attribute"
        className="h-8 w-40 rounded-md border bg-surface px-2 font-mono text-[12.5px] text-ink focus:border-brand focus:outline-none"
      />
      <datalist id={listId}>
        {(options ?? [])
          .filter((o: { name?: string }) => o.name)
          .map((o: { name?: string }) => (
            <option key={o.name} value={o.name} />
          ))}
      </datalist>
    </>
  )
}
