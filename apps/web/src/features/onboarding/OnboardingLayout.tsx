import type { ReactNode } from "react";

import { OnboardingStage, type StageScene, type TourChapter } from "./OnboardingStage";
import "./onboarding.css";

/**
 * Signing in and the console tour: the dark stage on the left, the panel on
 * the right with the page controls above it.
 */
export function OnboardingLayout({ scene, chapter, controls, children }: {
  /** What the stage shows; null while the console is still checking the sign-in. */
  scene: StageScene | null;
  chapter?: TourChapter;
  /** Theme, language and sign-out controls. */
  controls: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="onboarding">
      <OnboardingStage scene={scene} chapter={chapter} />
      <main className="onboarding-panel" id="main-content" tabIndex={-1}>
        <header className="onboarding-panel-head">
          <div className="onboarding-controls">{controls}</div>
        </header>
        <div className="onboarding-panel-body">{children}</div>
      </main>
    </div>
  );
}
