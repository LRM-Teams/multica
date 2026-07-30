import { cn } from "../../lib/utils";

const shared = "fill-current";

export function FleetClassIcon({
  classId,
  className,
}: {
  classId: string;
  className?: string;
}) {
  switch (classId) {
    case "dreadnought":
      return (
        <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
          <path className={shared} d="M4 16h16l-2 4H6l-2-4zm2-2 2-6h8l2 6H6zm3-6 1-3h4l1 3H9z" />
        </svg>
      );
    case "battleship":
      return (
        <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
          <path className={shared} d="M3 15h18l-1.5 3h-15L3 15zm3-3 2-5h8l2 5H6z" />
        </svg>
      );
    case "cruiser":
      return (
        <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
          <path className={shared} d="M4 14h16l-1 3H5l-1-3zm2-3 1.5-4h9L18 11H6z" />
        </svg>
      );
    case "frigate":
      return (
        <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
          <path className={shared} d="M5 14h14l-1 2H6l-1-2zm1.5-2 1-3h9l1 3h-11z" />
        </svg>
      );
    case "corvette":
      return (
        <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
          <path className={shared} d="M6 14h12l-.5 2h-11L6 14zm1-2 .5-2h9l.5 2H7z" />
        </svg>
      );
    default:
      return (
        <svg viewBox="0 0 24 24" className={cn("size-4", className)} aria-hidden>
          <path className={shared} d="M7 15h10l-1 2H8l-1-2zm1-2 1-3h8l1 3H8z" />
        </svg>
      );
  }
}
