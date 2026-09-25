import { Input } from '@/components/ui/primitives'
import { toISO, toLocalInput } from '@/lib/dates'

export function DateTimeInput({
  label,
  value,
  onChange,
}: {
  label: string
  value: string
  onChange: (iso: string) => void
}) {
  return (
    <Input
      type="datetime-local"
      aria-label={label}
      value={toLocalInput(value)}
      onChange={(e) => onChange(toISO(e.target.value))}
      className="h-8 w-52 font-mono"
    />
  )
}
