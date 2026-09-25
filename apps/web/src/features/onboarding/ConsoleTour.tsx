import { AnimatePresence } from "motion/react";
import * as m from "motion/react-m";
import { Activity, ArrowLeft, ArrowRight, Bot, FolderKanban, LayoutDashboard, ListTree, Server, Settings2, Trash2, Vault, type LucideIcon } from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { AppearanceMenu } from "../../components/AppearanceMenu";
import { ConsoleAccountMenu } from "../first-run/ConsoleAccess";
import { OnboardingLayout } from "./OnboardingLayout";
import type { TourChapter } from "./OnboardingStage";

export const TOUR_CHAPTERS: readonly TourChapter[] = ["monitor", "resources", "platform"];

const icons: Record<TourChapter, readonly [LucideIcon, LucideIcon, LucideIcon]> = {
  monitor: [LayoutDashboard, Activity, ListTree],
  resources: [Bot, Vault, Trash2],
  platform: [FolderKanban, Server, Settings2],
};

const EASE = [0.16, 1, 0.3, 1] as const;

/**
 * Opens the console tour from the console; receives the pressed button for the
 * reveal's origin. A button marked `data-tour-opener` gets the focus back when
 * the tour ends.
 */
export const ConsoleTourContext = createContext<(from: HTMLElement | null) => void>(() => undefined);
export const useConsoleTour = () => useContext(ConsoleTourContext);

/** The tour on the onboarding stage, in place of the console until it ends. */
export function ConsoleTourScreen({ onDone }: { onDone: (from: HTMLElement | null) => void }) {
  const [chapter, setChapter] = useState(0);
  return (
    <OnboardingLayout scene="tour" chapter={TOUR_CHAPTERS[chapter]} controls={<><ConsoleAccountMenu /><AppearanceMenu /></>}>
      <ConsoleTour chapter={chapter} onChapter={setChapter} onDone={onDone} />
    </OnboardingLayout>
  );
}

/**
 * An optional tour of the console: three short chapters, one per navigation
 * group, each beside a screenshot of those pages on the stage. The last
 * button, Skip and Escape return to the console.
 */
export function ConsoleTour({ chapter, onChapter, onDone }: {
  chapter: number;
  onChapter: (next: number) => void;
  /** Returns to the console; receives the pressed button for the reveal's origin. */
  onDone: (from: HTMLElement | null) => void;
}) {
  const { t } = useTranslation("onboarding");
  const id = TOUR_CHAPTERS[chapter] ?? "monitor";
  const last = chapter === TOUR_CHAPTERS.length - 1;
  const primary = useRef<HTMLButtonElement>(null);
  const points = t(`tour.chapters.${id}.points`, { returnObjects: true }) as readonly string[];
  const [First, Second, Third] = icons[id];
  const pointIcons = [First, Second, Third];

  // The tour replaces the page, so focus starts on its way forward.
  useEffect(() => { primary.current?.focus(); }, []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      // A control that already handled the key, such as Escape closing a menu, keeps it.
      if (event.defaultPrevented) return;
      if (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement) return;
      if (event.key === "ArrowRight" && !last) onChapter(chapter + 1);
      if (event.key === "ArrowLeft" && chapter > 0) onChapter(chapter - 1);
      if (event.key === "Escape") onDone(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [chapter, last, onChapter, onDone]);

  return (
    <section className="onboarding-tour" aria-labelledby="tour-heading">
      <div className="onboarding-tour-top">
        <span className="onboarding-eyebrow">{t("tour.eyebrow", { n: chapter + 1, total: TOUR_CHAPTERS.length })}</span>
        {!last ? <button className="onboarding-skip" type="button" onClick={(event) => onDone(event.currentTarget)}>{t("tour.skip")}</button> : null}
      </div>
      <AnimatePresence mode="wait" initial={false}>
        <m.div
          key={id}
          className="onboarding-tour-chapter"
          initial={{ opacity: 0, x: 24, filter: "blur(6px)" }}
          animate={{ opacity: 1, x: 0, filter: "blur(0px)" }}
          exit={{ opacity: 0, x: -24, filter: "blur(6px)" }}
          transition={{ duration: 0.38, ease: EASE }}
        >
          <span className="onboarding-chapter-name">{t(`tour.chapters.${id}.name`)}</span>
          <h2 id="tour-heading">{t(`tour.chapters.${id}.title`)}</h2>
          <ul className="onboarding-points">
            {points.map((point, index) => {
              const Icon = pointIcons[index] ?? First;
              return (
                <m.li
                  key={point}
                  initial={{ opacity: 0, y: 10 }}
                  animate={{ opacity: 1, y: 0 }}
                  transition={{ duration: 0.45, ease: EASE, delay: 0.12 + index * 0.08 }}
                >
                  <span className="onboarding-point-icon"><Icon size={15} strokeWidth={1.6} aria-hidden="true" /></span>
                  <span>{point}</span>
                </m.li>
              );
            })}
          </ul>
        </m.div>
      </AnimatePresence>
      <div className="onboarding-tour-foot">
        <div className="onboarding-dots" role="tablist" aria-label={t("tour.label")}>
          {TOUR_CHAPTERS.map((entry, index) => (
            <button
              key={entry}
              type="button"
              role="tab"
              aria-selected={index === chapter}
              aria-label={t(`tour.chapters.${entry}.name`)}
              className="onboarding-dot"
              onClick={() => onChapter(index)}
            >
              {index === chapter ? <m.span className="onboarding-dot-fill" layoutId="onboarding-dot" /> : null}
            </button>
          ))}
        </div>
        <div className="onboarding-tour-actions">
          {chapter > 0 ? (
            <button className="button ghost" type="button" onClick={() => onChapter(chapter - 1)}>
              <ArrowLeft size={14} aria-hidden="true" />{t("tour.back")}
            </button>
          ) : null}
          <button
            ref={primary}
            className={last ? "button primary onboarding-enter" : "button primary"}
            type="button"
            onClick={() => (last ? onDone(primary.current) : onChapter(chapter + 1))}
          >
            {last ? t("tour.done") : t("tour.next")}<ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </div>
    </section>
  );
}
