import { post } from './client';
import type { Asset } from './types';
export function changeAsset(
  id: string,
  action: 'quarantine' | 'restore' | 'recovery',
  reason: string,
  expectedVersion: number
) {
  return post<{ asset: Asset; changed: boolean }>(`/assets/${encodeURIComponent(id)}/${action}`, {
    reason,
    expectedVersion
  });
}
