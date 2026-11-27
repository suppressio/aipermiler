// renderer/instantFlow.ts — the arrows on the instant gauge (#304, part of #132): they
// show the instant trend computed by the core (D-3.28, `trend` of the pacing snapshot):
// the pace an hour's samples give now against the one 30 minutes earlier. They span the
// whole bar and point the way the pace is going: right while it is accelerating, left
// while it is slowing down; none when it is steady or cannot be measured. No gap to the
// target and no day-off rule any more (approach of #302). The renderer shows them only
// with the window animations on and no reduced motion (style.css, `.motion-effects`).

import type { QuotaWindowSnapshot } from '../types/index';

// A 48 px repeat at 18 px/s: one steady, low-key speed (the trend has no size to show).
export const FLOW_DURATION_SECONDS = 48 / 18;

export function trendFlow(trend: QuotaWindowSnapshot['trend']): { direction: 'left' | 'right'; durationSeconds: number } | null {
  if (trend === 'accelerating') return { direction: 'right', durationSeconds: FLOW_DURATION_SECONDS };
  if (trend === 'slowing-down') return { direction: 'left', durationSeconds: FLOW_DURATION_SECONDS };
  return null;
}
