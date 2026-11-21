// SelectAllCheckbox 是带半选态的全选复选框
export function SelectAllCheckbox({
  selected,
  total,
  onToggle,
  label,
}: {
  selected: number
  total: number
  onToggle: (next: boolean) => void
  label?: string
}) {
  const all = total > 0 && selected === total
  return (
    <input
      type="checkbox"
      className="size-4 align-middle accent-primary"
      aria-label={label}
      checked={all}
      disabled={total === 0}
      ref={(node) => {
        if (node) node.indeterminate = selected > 0 && !all
      }}
      onChange={() => {
        onToggle(!all)
      }}
    />
  )
}
