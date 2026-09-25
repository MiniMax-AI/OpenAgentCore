import { LazyMotion, MotionConfig } from "motion/react";
import type { ReactNode } from "react";

/** Motion's DOM features, including shared layout, loaded after first paint. */
const features = () => import("./motion-features").then((module) => module.default);

/**
 * The console's motion defaults. Motion (motion.dev) carries the shared-layout
 * moves — the active navigation chip and segmented thumbs — with one quick,
 * unbouncy spring; everything else is CSS. Reduced motion follows the
 * operating system: layout moves become instant, opacity changes remain.
 */
export const LAYOUT_SPRING = { type: "spring", duration: 0.32, bounce: 0 } as const;

export function ConsoleMotion({ children }: { children: ReactNode }) {
  return (
    <MotionConfig reducedMotion="user" transition={LAYOUT_SPRING}>
      <LazyMotion features={features} strict>{children}</LazyMotion>
    </MotionConfig>
  );
}
