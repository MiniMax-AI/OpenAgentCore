export interface RuntimeTrendTarget {
  seriesId: string;
  label: string;
  cpuRatio: number | null;
}

export interface RuntimeTrendSample {
  sampledAt: number;
  activeSandboxCount: number | null;
  targets: RuntimeTrendTarget[];
  memoryUsageBytes: number | null;
  memoryLimitBytes: number | null;
  tokenTotals: RuntimeTrendTokenTotal[];
  inputTokensPerMinute: number | null;
  outputTokensPerMinute: number | null;
}

export interface RuntimeTrendTokenTotal {
  sessionId: string;
  sampledAt: number;
  inputTokens: number;
  outputTokens: number;
}

function tokenRate(
  previous: RuntimeTrendSample,
  next: RuntimeTrendSample,
): Pick<RuntimeTrendSample, "inputTokensPerMinute" | "outputTokensPerMinute"> {
  const previousTotals = new Map(previous.tokenTotals.map((total) => [total.sessionId, total]));
  if (previousTotals.size === 0 || previousTotals.size !== next.tokenTotals.length ||
      next.tokenTotals.some((current) => !previousTotals.has(current.sessionId))) {
    return { inputTokensPerMinute: null, outputTokensPerMinute: null };
  }
  const pairs = next.tokenTotals.flatMap((current): Array<[RuntimeTrendTokenTotal, RuntimeTrendTokenTotal]> => {
    const prior = previousTotals.get(current.sessionId);
    return prior ? [[prior, current]] : [];
  });
  const inputRates = pairs.map(([prior, current]) => {
    const elapsedMinutes = (current.sampledAt - prior.sampledAt) / 60_000;
    const delta = current.inputTokens - prior.inputTokens;
    return elapsedMinutes > 0 && delta >= 0 ? delta / elapsedMinutes : null;
  });
  const outputRates = pairs.map(([prior, current]) => {
    const elapsedMinutes = (current.sampledAt - prior.sampledAt) / 60_000;
    const delta = current.outputTokens - prior.outputTokens;
    return elapsedMinutes > 0 && delta >= 0 ? delta / elapsedMinutes : null;
  });
  const inputRate = inputRates.length > 0 && inputRates.every((rate) => rate !== null)
    ? inputRates.reduce<number>((total, rate) => total + (rate ?? 0), 0)
    : null;
  const outputRate = outputRates.length > 0 && outputRates.every((rate) => rate !== null)
    ? outputRates.reduce<number>((total, rate) => total + (rate ?? 0), 0)
    : null;
  return {
    inputTokensPerMinute: inputRate,
    outputTokensPerMinute: outputRate,
  };
}

export function deriveTokenThroughput(samples: readonly RuntimeTrendSample[]): RuntimeTrendSample[] {
  return samples.map((sample, index) => {
    const current = { ...sample };
    const previous = samples[index - 1];
    if (previous) Object.assign(current, tokenRate(previous, sample));
    return current;
  });
}
