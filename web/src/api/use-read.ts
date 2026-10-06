import { useEffect, useRef, useState } from 'react';
import { ApiError, request } from './client';

export function useRead<T>(path: string | null, revision = 0, pollMs = 0) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<ApiError | Error | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [lastSuccess, setLastSuccess] = useState<Date | null>(null);
  const previousPath = useRef<string | null>(null);
  const sequence = useRef(0);
  useEffect(() => {
    const generation = ++sequence.current;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | null = null;
    let stopped = false;
    let running = false;
    if (previousPath.current !== path) {
      setData(null);
      setError(null);
      setLastSuccess(null);
    }
    previousPath.current = path;
    if (!path) {
      setRefreshing(false);
      return;
    }
    const load = async () => {
      if (running || stopped || document.hidden) return;
      running = true;
      controller = new AbortController();
      setRefreshing(true);
      try {
        const result = await request<T>(path, { signal: controller.signal });
        if (!stopped && generation === sequence.current) {
          setData(result);
          setError(null);
          setLastSuccess(new Date());
        }
      } catch (e) {
        if (
          !stopped &&
          generation === sequence.current &&
          !(e instanceof DOMException && e.name === 'AbortError')
        )
          setError(e instanceof Error ? e : new Error('Refresh failed.'));
      } finally {
        running = false;
        if (!stopped && generation === sequence.current) {
          setRefreshing(false);
          if (pollMs) timer = setTimeout(() => void load(), pollMs);
        }
      }
    };
    const visibility = () => {
      if (document.hidden) {
        clearTimeout(timer);
        controller?.abort();
      } else {
        clearTimeout(timer);
        void load();
      }
    };
    document.addEventListener('visibilitychange', visibility);
    void load();
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller?.abort();
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [path, revision, pollMs]);
  const sameResource = previousPath.current === path;
  return {
    data: sameResource ? data : null,
    error: sameResource ? error : null,
    refreshing,
    lastSuccess: sameResource ? lastSuccess : null
  };
}
