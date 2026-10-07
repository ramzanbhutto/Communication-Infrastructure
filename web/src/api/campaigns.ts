import type { Asset, Audit } from './types';
export type CampaignStep = {
  channel: 'email' | 'sms';
  subject: string;
  body: string;
  delaySeconds: number;
};
export type Campaign = {
  id: string;
  name: string;
  state: 'draft' | 'active' | 'paused' | 'cancelled';
  version: number;
  steps: CampaignStep[];
  assetIds: string[];
  timezone: string;
  startHour: number;
  endHour: number;
  weekdaysOnly: boolean;
  createdAt: string;
  updatedAt: string;
};
export type CampaignInput = Pick<
  Campaign,
  'name' | 'steps' | 'assetIds' | 'timezone' | 'startHour' | 'endHour' | 'weekdaysOnly'
> & { expectedVersion?: number };
export type CampaignList = {
  items: Campaign[];
  summaries: Record<string, Record<string, number>>;
  assets: Asset[];
  total: number;
  page: number;
  pageSize: number;
  observedAt: string;
};
export type CampaignDetail = {
  campaign: Campaign;
  enrollments: {
    id: string;
    contactId: string;
    timezone: string;
    step: number;
    state: string;
    reason: string;
    nextRunAt: string;
    enrolledAt: string;
    updatedAt: string;
  }[];
  executions: {
    enrollmentId: string;
    step: number;
    jobId: string;
    state: string;
    assetId: string;
    decisionId: string | null;
    createdAt: string;
  }[];
  audit: Audit[];
  total: number;
  page: number;
  pageSize: number;
  observedAt: string;
};
