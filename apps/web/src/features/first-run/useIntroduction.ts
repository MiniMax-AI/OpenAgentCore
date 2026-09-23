import { useMemo, useState } from "react";
import { isLocalProxyBaseUrl } from "../../lib/connection";
import { useConsoleAccount } from "./ConsoleAccess";
import { progressKey, readProgress, saveProgress, type IntroductionProgress } from "./progress";

export function useIntroduction(baseUrl: string) {
  const account = useConsoleAccount();
  const key = account ? progressKey(window.location.origin, account.username) : "";
  const initial = useMemo(() => key ? readProgress(key) : null, [key]);
  const [override, setOverride] = useState<{ key: string; progress: IntroductionProgress } | null>(null);
  const progress = override?.key === key ? override.progress : initial;
  const update = (next: IntroductionProgress) => { setOverride({ key, progress: next }); if (key) saveProgress(key, next); };
  return {
    available: Boolean(account) && isLocalProxyBaseUrl(baseUrl),
    visible: Boolean(progress && !progress.dismissed),
    username: account?.username,
    step: progress?.step ?? 0,
    setStep: (step: 0 | 1 | 2) => update({ step, dismissed: false }),
    dismiss: () => update({ step: progress?.step ?? 0, dismissed: true }),
    replay: () => update({ step: 0, dismissed: false }),
  };
}
