import { post } from './client';
import type { OutreachResult } from './types';
export function outreach(
  channel: 'call' | 'sms',
  contactId: string,
  lineId: string,
  message: string,
  requestKey: string
) {
  return post<OutreachResult>(channel === 'call' ? '/dial' : '/sms', {
    contactId,
    lineId,
    message,
    requestKey
  });
}
export function setReplyConsumer(paused: boolean) {
  return post<{ paused: boolean }>('/demo/replies', { paused });
}
