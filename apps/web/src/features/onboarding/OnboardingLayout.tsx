import * as m from "motion/react-m";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { OnboardingStage, type StageScene, type TourChapter } from "./OnboardingStage";
import "./onboarding.css";

export type OnboardingStep = "project" | "tour";
const STEPS: readonly OnboardingStep[] = ["project", "tour"];

/**
 * Every onboarding screen: the dark stage on the left, the step's panel on
 * the right with setup progress and the page controls above it. Signing in
 * (no `step`) uses the same frame without the progress.
 */
export function OnboardingLayout({ scene, chapter, step, controls, children }: {
  /** What the stage shows; null while the console is still checking the sign-in. */
  scene: StageScene | "tour" | null;
  chapter?: TourChapter;
  /** The first-run step this screen is; omitted when signing in. */
  step?: OnboardingStep;
  /** Theme, language and sign-out controls. */
  controls: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="onboarding">
      <OnboardingStage scene={scene} chapter={chapter} />
      <main className="onboarding-panel" id="main-content" tabIndex={-1}>
        <header className="onboarding-panel-head">
          {step ? <Progress step={step} /> : <span />}
          <div className="onboarding-controls">{controls}</div>
        </header>
        <div className="onboarding-panel-body">{children}</div>
      </main>
    </div>
  );
}

function Progress({ step }: { step: OnboardingStep }) {
  const { t } = useTranslation("onboarding");
  const current = STEPS.indexOf(step);
  return (
    <ol className="onboarding-progress" aria-label={t("steps.label")}>
      {STEPS.map((entry, index) => (
        <li key={entry} className={index < current ? "done" : index === current ? "current" : undefined} aria-current={index === current ? "step" : undefined}>
          <span className="onboarding-progress-mark">
            {index === current ? <m.span className="onboarding-progress-fill" layoutId="onboarding-progress" /> : null}
            <span className="onboarding-progress-number">{index + 1}</span>
          </span>
          <span className="onboarding-progress-label">{t(`steps.${entry}`)}</span>
        </li>
      ))}
    </ol>
  );
}
