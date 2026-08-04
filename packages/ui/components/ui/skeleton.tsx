import { cn } from "@multica/ui/lib/utils"

/**
 * Loading placeholder.
 *
 * LRM-1366: the fill is `bg-skeleton`, never `bg-muted`. In the light theme
 * `--muted` resolves to `--page-bg`, which is also `--sidebar` — a `bg-muted`
 * placeholder was invisible on the conversation list pane (1.00:1) and
 * near-invisible on white (1.03:1), so a pending DM/channel list read as a
 * blank region instead of a loading one.
 */
function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      className={cn("animate-pulse rounded-md bg-skeleton", className)}
      {...props}
      aria-hidden="true"
    />
  )
}

export { Skeleton }
