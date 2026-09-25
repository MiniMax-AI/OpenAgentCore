import { AnimatePresence } from "motion/react";
import * as m from "motion/react-m";
import { Bot, FileText, KeyRound, Layers3, MessagesSquare, Puzzle, Server, Vault } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { BorderBeam } from "@/components/magicui/border-beam";
import { FlickeringGrid } from "@/components/magicui/flickering-grid";
import { LightRays } from "@/components/magicui/light-rays";
import { OrbitingCircles } from "@/components/magicui/orbiting-circles";

export type StageScene = "login" | "project";
export type TourChapter = "monitor" | "resources" | "platform";

const EASE = [0.16, 1, 0.3, 1] as const;

/**
 * The dark stage beside every onboarding step. Its backdrop (a flickering
 * indigo grid under slow light rays) stays the same throughout; in front of
 * it, Core — the Parsar mark — holds its orbits of Agents, Sessions and the
 * rest, and during the tour a screenshot of the console takes its place.
 */
export function OnboardingStage({ scene, chapter }: { scene: StageScene | "tour" | null; chapter?: TourChapter }) {
  return (
    <aside className="onboarding-stage">
      <div className="onboarding-backdrop" aria-hidden="true">
        <FlickeringGrid className="onboarding-grid" squareSize={3} gridGap={7} color="rgb(129, 140, 248)" maxOpacity={0.28} flickerChance={0.1} />
        <LightRays color="rgba(129, 140, 248, 0.16)" count={6} blur={42} speed={16} length="80vh" />
        <div className="onboarding-glow" />
      </div>
      <div className="onboarding-brand">
        <img src="/parsar-mark-dark.png" width="22" height="22" alt="" aria-hidden="true" />
        <span>Parsar Core</span>
      </div>
      {scene === "tour" && chapter ? <TourShowcase chapter={chapter} /> : <Constellation scene={scene === "tour" ? null : scene} />}
    </aside>
  );
}

function Constellation({ scene }: { scene: StageScene | null }) {
  const { t } = useTranslation("onboarding");
  const chip = (icon: ReactNode, key: string) => <span className="onboarding-chip" key={key}>{icon}</span>;
  return (
    <>
      <div className="onboarding-orbits" role="img" aria-label={t("stage.orbit")}>
        <OrbitingCircles radius={112} iconSize={38} duration={34}>
          {chip(<Bot size={17} strokeWidth={1.6} />, "agent")}
          {chip(<MessagesSquare size={17} strokeWidth={1.6} />, "session")}
          {chip(scene === "project" ? <KeyRound size={17} strokeWidth={1.6} /> : <Puzzle size={17} strokeWidth={1.6} />, "inner")}
        </OrbitingCircles>
        <OrbitingCircles radius={196} iconSize={38} duration={52} reverse>
          {chip(<Vault size={17} strokeWidth={1.6} />, "vault")}
          {chip(<FileText size={17} strokeWidth={1.6} />, "file")}
          {chip(<Layers3 size={17} strokeWidth={1.6} />, "template")}
          {chip(<Server size={17} strokeWidth={1.6} />, "node")}
        </OrbitingCircles>
        <m.div
          className="onboarding-core"
          initial={{ opacity: 0, scale: 0.86 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 0.7, ease: EASE }}
        >
          <img src="/parsar-mark-dark.png" width="40" height="40" alt="" />
          <BorderBeam size={70} duration={7} colorFrom="#818cf8" colorTo="#e879f9" borderWidth={1.5} />
        </m.div>
      </div>
      {scene ? <StageCopy scene={scene} /> : null}
    </>
  );
}

function StageCopy({ scene }: { scene: StageScene }) {
  const { t } = useTranslation("onboarding");
  // Keyed, not exit-animated: a new scene simply blurs in over the old one.
  return (
    <div className="onboarding-copy">
      <m.div
        key={scene}
        initial={{ opacity: 0, y: 14, filter: "blur(8px)" }}
        animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
        transition={{ duration: 0.6, ease: EASE }}
      >
        {/* Brand copy, not a heading: the panel's title names the task. */}
        <p className="onboarding-headline">{t(`stage.${scene}.title`)}</p>
        <p>{t(`stage.${scene}.body`)}</p>
      </m.div>
    </div>
  );
}

/** A real screenshot of the chapter's pages, tilted in the stage's light. */
function TourShowcase({ chapter }: { chapter: TourChapter }) {
  const { t, i18n } = useTranslation("onboarding");
  const language = i18n.resolvedLanguage?.startsWith("zh") ? "zh" : "en";
  return (
    <div className="onboarding-showcase">
      <AnimatePresence mode="popLayout" initial={true}>
        <m.figure
          key={chapter}
          className="onboarding-shot"
          initial={{ opacity: 0, x: 80, rotateY: -18, filter: "blur(10px)" }}
          animate={{ opacity: 1, x: 0, rotateY: -8, filter: "blur(0px)" }}
          exit={{ opacity: 0, x: -80, rotateY: 4, filter: "blur(10px)" }}
          transition={{ duration: 0.75, ease: EASE }}
        >
          <img src={`/onboarding/${chapter}-${language}.webp`} width="1200" height="750" alt={t("tour.shot", { name: t(`tour.chapters.${chapter}.name`) })} />
          <BorderBeam size={140} duration={9} colorFrom="#818cf8" colorTo="#e879f9" borderWidth={1.5} />
        </m.figure>
      </AnimatePresence>
    </div>
  );
}
