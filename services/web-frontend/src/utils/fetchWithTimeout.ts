const TIMEOUT_MESSAGE = "서버 응답 시간이 초과되었습니다. 잠시 후 다시 시도해 주세요.";

export function isTimeoutError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

export function timeoutMessage(): string {
  return TIMEOUT_MESSAGE;
}

/**
 * fetch with AbortController timeout.
 * If an external signal is provided, abort from either source cancels the request.
 */
export function fetchWithTimeout(
  url: string,
  options: RequestInit & { timeoutMs?: number } = {},
): Promise<Response> {
  const { timeoutMs = 10_000, signal: externalSignal, ...rest } = options;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);

  // If caller provides its own signal (e.g. useEffect cleanup), forward its abort
  if (externalSignal) {
    if (externalSignal.aborted) {
      controller.abort();
    } else {
      externalSignal.addEventListener("abort", () => controller.abort(), { once: true });
    }
  }

  return fetch(url, { ...rest, signal: controller.signal }).finally(() => {
    clearTimeout(timer);
  });
}
