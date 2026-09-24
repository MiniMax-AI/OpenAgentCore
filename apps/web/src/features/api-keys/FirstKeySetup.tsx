/** Placeholder until the first-run flow creates the first API key through the Web API. */
export function FirstKeySetup({ onDone }: { onDone: () => void }) {
  return <button type="button" className="button primary" onClick={onDone}>Continue</button>;
}
