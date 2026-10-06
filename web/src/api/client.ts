import { validReadShape } from './response-shape';

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public decisionId?: string,
    public details?: unknown
  ) {
    super(message);
  }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    const timeout = AbortSignal.timeout(12_000);
    const signal = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;
    response = await fetch(`/api/v1${path}`, {
      credentials: 'same-origin',
      ...options,
      signal,
      headers: {
        ...(options.body ? { 'Content-Type': 'application/json' } : {}),
        ...options.headers
      }
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    throw new ApiError(
      0,
      'NETWORK_UNCONFIRMED',
      'The connection failed. The result is not confirmed. Refresh recorded activity before submitting another action.'
    );
  }
  const data = await response.json().catch(() => null);
  if (!response.ok)
    throw new ApiError(
      response.status,
      data?.code ?? 'REQUEST_FAILED',
      data?.message ?? 'The server could not confirm this request.',
      data?.decisionId,
      data?.details
    );
  if (
    data === null ||
    typeof data !== 'object' ||
    Array.isArray(data) ||
    ((!options.method || options.method === 'GET') && !validReadShape(path, data))
  )
    throw new ApiError(
      response.status,
      'INVALID_RESPONSE',
      'The server returned an unreadable response.'
    );
  return data as T;
}

// Mutations are sent once. Only an explicit retry with the same request key is safe.
export function post<T>(path: string, body: unknown): Promise<T> {
  return request<T>(path, { method: 'POST', body: JSON.stringify(body) });
}
export function message(error: unknown): string {
  return error instanceof Error ? error.message : 'This operation could not be confirmed.';
}

export async function downloadDecisions(query: string): Promise<void> {
  const response = await fetch(`/api/v1/decisions/export?${query}`, { credentials: 'same-origin' });
  if (!response.ok) {
    const data = await response.json().catch(() => null);
    throw new ApiError(
      response.status,
      data?.code ?? 'EXPORT_FAILED',
      data?.message ?? 'The export could not be created.'
    );
  }
  const href = URL.createObjectURL(await response.blob());
  const anchor = document.createElement('a');
  anchor.href = href;
  anchor.download = 'decisions-last-24h-masked.csv';
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(href), 1000);
}
