/* eslint-disable react-refresh/only-export-components */
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { tr } from "../../lib/i18n";

import { cn } from "../../lib/utils";

/** One user turn ("floor") in a thread. */
export interface TurnEntry {
  /** Matches the `data-turn-key` of the turn's element inside the viewport. */
  key: string;
  /** First line of the turn, already collapsed and truncated. */
  preview: string;
}

const MIN_MARKERS = 8;
const MAX_MARKERS = 40;
/** 2px bar + 7.5px gap. */
const SLOT_PX = 9.5;
const REST_WIDTH_PX = 6;
const ACTIVE_WIDTH_PX = 26;
const WAVE_WIDTHS_PX = [26, 20, 14, 10] as const;
const PREVIEW_HALF_HEIGHT_PX = 32;
const LEAVE_DELAY_MS = 160;
const POP_OUT_MS = 150;
const PREVIEW_MAX_CHARS = 96;

/** Whitespace-collapsed head of a turn for markers and the preview. */
export function turnPreview(text: string, max = PREVIEW_MAX_CHARS): string {
  const collapsed = text.replace(/\s+/g, " ").trim();
  if (!collapsed) return "…";
  const chars = Array.from(collapsed);
  return chars.length > max ? `${chars.slice(0, max).join("")}…` : collapsed;
}

/**
 * Even sampling of turns to `budget` markers (first and last always kept),
 * with the active turn swapped in for its nearest sampled neighbour so the
 * marker count and column stay put while the highlight follows the scroll.
 */
export function sampleTurns<T extends { key: string }>(
  turns: T[],
  budget: number,
  activeKey: string | null,
): T[] {
  if (budget <= 0) return [];
  if (turns.length <= budget) return turns;
  const last = turns.length - 1;
  const picked: number[] = [];
  for (let i = 0; i < budget; i++) {
    const index = Math.round((i * last) / (budget - 1 || 1));
    if (picked[picked.length - 1] !== index) picked.push(index);
  }
  const activeIndex = activeKey
    ? turns.findIndex((turn) => turn.key === activeKey)
    : -1;
  if (activeIndex >= 0 && !picked.includes(activeIndex)) {
    let nearest = 0;
    for (let i = 1; i < picked.length; i++) {
      if (
        Math.abs(picked[i]! - activeIndex) <
        Math.abs(picked[nearest]! - activeIndex)
      )
        nearest = i;
    }
    picked[nearest] = activeIndex;
    picked.sort((a, b) => a - b);
  }
  return picked.map((index) => turns[index]!);
}

/**
 * The turn whose element sits nearest the viewport's top third: the last
 * turn that starts above that line, else the first. Recomputed on scroll
 * (rAF-throttled), on viewport resize and whenever the keys change.
 */
export function useActiveTurnKey(
  viewport: HTMLElement | null,
  keys: readonly string[],
): string | null {
  const [active, setActive] = useState<string | null>(null);
  const keyList = keys.join("\u0000");

  useEffect(() => {
    if (!viewport) return;
    let frame: number | null = null;
    const compute = () => {
      frame = null;
      const line =
        viewport.getBoundingClientRect().top + viewport.clientHeight / 3;
      const nodes = viewport.querySelectorAll<HTMLElement>("[data-turn-key]");
      let found: string | null = null;
      for (const node of nodes) {
        if (node.getBoundingClientRect().top <= line)
          found = node.dataset.turnKey ?? null;
        else break;
      }
      if (found === null && nodes.length > 0)
        found = nodes[0]!.dataset.turnKey ?? null;
      setActive((prev) => (prev === found ? prev : found));
    };
    const schedule = () => {
      if (frame !== null) return;
      frame = window.requestAnimationFrame(compute);
    };
    compute();
    viewport.addEventListener("scroll", schedule, { passive: true });
    const observer =
      typeof ResizeObserver === "undefined"
        ? null
        : new ResizeObserver(schedule);
    observer?.observe(viewport);
    return () => {
      viewport.removeEventListener("scroll", schedule);
      observer?.disconnect();
      if (frame !== null) window.cancelAnimationFrame(frame);
    };
  }, [viewport, keyList]);

  return active;
}

function markerWidth(
  index: number,
  hovered: number,
  isActive: boolean,
): number {
  if (hovered >= 0)
    return WAVE_WIDTHS_PX[Math.abs(index - hovered)] ?? REST_WIDTH_PX;
  return isActive ? ACTIVE_WIDTH_PX : REST_WIDTH_PX;
}

/**
 * A vertical rail on the left edge of a thread: one 2px marker per user
 * turn, the active one in ink at 26px, a wave under the pointer, a floating
 * preview beside the hovered marker, click to scroll the turn into view.
 * Hidden below the console's 960px floor and with fewer than two turns.
 */
export function TurnNavRail({
  turns,
  activeKey,
  onJump,
  className,
}: {
  turns: TurnEntry[];
  activeKey: string | null;
  /** Default: scrolls the `[data-turn-key]` element into view. */
  onJump?: (key: string) => void;
  className?: string;
}) {
  const [navEl, setNavEl] = useState<HTMLElement | null>(null);
  const [budget, setBudget] = useState(MAX_MARKERS);
  const [hoveredKey, setHoveredKey] = useState<string | null>(null);
  const [previewKey, setPreviewKey] = useState<string | null>(null);
  const [previewClosing, setPreviewClosing] = useState(false);
  const leaveTimer = useRef<number | null>(null);
  const closeTimer = useRef<number | null>(null);

  useLayoutEffect(() => {
    if (!navEl || typeof ResizeObserver === "undefined") return;
    const update = () => {
      const fit = Math.floor((navEl.clientHeight - 16) / SLOT_PX);
      setBudget(Math.max(MIN_MARKERS, Math.min(MAX_MARKERS, fit)));
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(navEl);
    return () => observer.disconnect();
  }, [navEl]);

  const markers = useMemo(
    () => sampleTurns(turns, budget, activeKey),
    [turns, budget, activeKey],
  );
  const activeMarkerKey = useMemo(() => {
    if (!activeKey || markers.length === 0) return null;
    if (markers.some((m) => m.key === activeKey)) return activeKey;
    const activeIndex = turns.findIndex((turn) => turn.key === activeKey);
    let nearest: string | null = null;
    let distance = Number.POSITIVE_INFINITY;
    for (const marker of markers) {
      const d = Math.abs(
        turns.findIndex((turn) => turn.key === marker.key) - activeIndex,
      );
      if (d < distance) {
        distance = d;
        nearest = marker.key;
      }
    }
    return nearest;
  }, [activeKey, markers, turns]);

  const hoveredIndex = markers.findIndex((m) => m.key === hoveredKey);
  const previewIndex = markers.findIndex((m) => m.key === previewKey);
  const previewTurn = previewIndex >= 0 ? markers[previewIndex] : null;
  const previewOffset =
    previewIndex >= 0 ? (previewIndex - (markers.length - 1) / 2) * SLOT_PX : 0;
  const previewTop =
    previewIndex >= 0
      ? `clamp(${PREVIEW_HALF_HEIGHT_PX}px, calc(50% ${previewOffset < 0 ? "-" : "+"} ${Math.abs(previewOffset)}px), calc(100% - ${PREVIEW_HALF_HEIGHT_PX}px))`
      : "50%";

  const clearTimers = useCallback(() => {
    if (leaveTimer.current !== null) {
      window.clearTimeout(leaveTimer.current);
      leaveTimer.current = null;
    }
  }, []);

  const enter = useCallback(
    (key: string) => {
      clearTimers();
      if (closeTimer.current !== null) {
        window.clearTimeout(closeTimer.current);
        closeTimer.current = null;
      }
      setPreviewClosing(false);
      setHoveredKey(key);
      setPreviewKey(key);
    },
    [clearTimers],
  );

  const leave = useCallback(() => {
    clearTimers();
    leaveTimer.current = window.setTimeout(() => {
      leaveTimer.current = null;
      setHoveredKey(null);
      setPreviewClosing(true);
      closeTimer.current = window.setTimeout(() => {
        closeTimer.current = null;
        setPreviewKey(null);
        setPreviewClosing(false);
      }, POP_OUT_MS);
    }, LEAVE_DELAY_MS);
  }, [clearTimers]);

  useEffect(
    () => () => {
      clearTimers();
      if (closeTimer.current !== null) window.clearTimeout(closeTimer.current);
    },
    [clearTimers],
  );

  const jump = useCallback(
    (key: string) => {
      if (onJump) {
        onJump(key);
        return;
      }
      const escaped =
        typeof CSS !== "undefined" && CSS.escape
          ? CSS.escape(key)
          : key.replace(/"/g, '\\"');
      document
        .querySelector<HTMLElement>(`[data-turn-key="${escaped}"]`)
        ?.scrollIntoView({ block: "start", behavior: "smooth" });
    },
    [onJump],
  );

  if (turns.length < 2) return null;

  return (
    <nav
      ref={setNavEl}
      aria-label={tr("对话轮次")}
      className={cn(
        "pointer-events-none absolute inset-y-0 left-0 z-10 hidden w-5 items-center min-[960px]:flex",
        className,
      )}
      onMouseEnter={clearTimers}
      onMouseLeave={leave}
      onBlur={(event) => {
        if (
          event.relatedTarget instanceof Node &&
          event.currentTarget.contains(event.relatedTarget)
        )
          return;
        leave();
      }}
      onKeyDown={(event) => {
        if (event.key !== "Escape") return;
        clearTimers();
        setHoveredKey(null);
        setPreviewKey(null);
      }}
    >
      <div className="pointer-events-auto flex max-h-full flex-col items-end gap-[7.5px] py-2 pr-0.5 pl-1">
        {markers.map((turn, index) => {
          const isActive = turn.key === activeMarkerKey;
          const isHovered = index === hoveredIndex;
          return (
            <button
              key={turn.key}
              type="button"
              aria-label={turn.preview}
              aria-current={isActive ? "location" : undefined}
              onClick={() => jump(turn.key)}
              onMouseEnter={() => enter(turn.key)}
              onFocus={() => enter(turn.key)}
              className={cn(
                "relative h-0.5 rounded-full transition-[width,background-color] duration-150 ease-settle after:absolute after:-inset-x-2 after:-inset-y-1 after:content-[''] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40",
                isHovered || isActive ? "bg-fg" : "bg-line-strong",
              )}
              style={{ width: markerWidth(index, hoveredIndex, isActive) }}
            />
          );
        })}
      </div>

      {previewTurn && (
        <div
          role="presentation"
          className={cn(
            "app-shadow-floating pointer-events-auto absolute left-full z-20 ml-1.5 w-64 -translate-y-1/2 rounded-lg border border-line bg-surface p-2",
            previewClosing ? "animate-pop-out" : "animate-pop-in",
          )}
          style={{ top: previewTop }}
          onMouseEnter={clearTimers}
        >
          <button
            type="button"
            onClick={() => jump(previewTurn.key)}
            className="block w-full rounded-md text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          >
            <span className="line-clamp-2 break-words text-xs text-fg-muted">
              {previewTurn.preview}
            </span>
          </button>
        </div>
      )}
    </nav>
  );
}
