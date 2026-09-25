import { useReducedMotion } from "motion/react";
import { Check, Copy } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Terminal, TypingAnimation } from "@/components/magicui/terminal";
import { CommandBlock, useCopy } from "../api-keys/IssuedKey";

/**
 * The example request, typed line by line into a terminal. With reduced
 * motion it is the plain command block. Either way it can be copied.
 */
export function RequestTerminal({ value, label }: { value: string; label: string }) {
  const { t } = useTranslation("keys");
  const reduced = useReducedMotion();
  const { state, copy } = useCopy(value);
  if (reduced) return <CommandBlock value={value} label={label} />;
  const copied = state === "copied";
  return (
    <div className="onboarding-terminal" role="group" aria-label={label}>
      <Terminal>
        {value.split("\n").map((line, index) => (
          <TypingAnimation key={index} duration={16}>{line}</TypingAnimation>
        ))}
      </Terminal>
      <button
        type="button"
        className="icon-button ghost onboarding-terminal-copy"
        aria-label={copied ? t("issued.copied") : t("issued.copyCommand")}
        title={copied ? t("issued.copied") : t("issued.copyCommand")}
        onClick={() => void copy()}
      >
        {copied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
      </button>
    </div>
  );
}
