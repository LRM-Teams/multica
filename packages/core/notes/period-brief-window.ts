/**
 * Period Brief window helpers — custom inclusive calendar ranges.
 */

const YMD = /^\d{4}-\d{2}-\d{2}$/;

/** Lexicographic compare is safe for YYYY-MM-DD. */
export function isValidPeriodBriefCustomRange(startDate: string, endDate: string): boolean {
  const start = startDate.trim();
  const end = endDate.trim();
  if (!YMD.test(start) || !YMD.test(end)) return false;
  return start <= end;
}

/** Shift a YYYY-MM-DD calendar day by `delta` days (UTC date arithmetic). */
export function shiftPeriodBriefCalendarDay(dayYYYYMMDD: string, delta: number): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dayYYYYMMDD.trim());
  if (!match) return dayYYYYMMDD;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const utc = new Date(Date.UTC(year, month - 1, day + delta));
  return utc.toISOString().slice(0, 10);
}

/** Default custom range: last 7 calendar days ending on `today` (inclusive). */
export function defaultPeriodBriefCustomRange(todayYYYYMMDD: string): {
  start_date: string;
  end_date: string;
} {
  const end = todayYYYYMMDD.trim();
  return {
    start_date: shiftPeriodBriefCalendarDay(end, -6),
    end_date: end,
  };
}
