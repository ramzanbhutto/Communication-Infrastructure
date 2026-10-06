import type { Asset } from './types';
export type DeliveryChannel = 'sms' | 'call' | 'email' | 'provision' | 'trunk' | 'imessage';
export type Scenario = 'success' | 'throttle' | 'ambiguous' | 'filtered' | 'bounce';
export type DeliveryJob = {
  decisionId: string | null;
  id: string;
  channel: DeliveryChannel;
  contactId: string | null;
  assetId: string | null;
  state: string;
  attempts: number;
  providerId: string | null;
  lastError: string | null;
  subject: string;
  destination: string;
  purpose: string;
  scenario: Scenario;
  createdAt: string;
  updatedAt: string;
  nextAttemptAt: string;
};
export type DNSRecord = { name: string; state: string; values: string[]; error: string | null };
export type DNSAssessment = {
  source: string;
  spf: DNSRecord;
  dkim: DNSRecord;
  dmarc: DNSRecord;
  limitation: string;
};
export type InboxItem = {
  id: string;
  contactId: string;
  assetId: string;
  channel: 'sms' | 'email' | 'imessage';
  state: string;
  version: number;
  receivedAt: string;
  source: string;
};
export type InfrastructureData = {
  sipProbe: {
    state: string;
    transport: string;
    status: string;
    roundTripMs: number;
    checkedAt: string;
    source: string;
    limitation: string;
  } | null;
  deliveryWorker: { state: string; lastSuccessfulTick: string | null };
  mode: 'disabled' | 'local_lab';
  liveEnabled: false;
  imessageEnabled: boolean;
  jobs: DeliveryJob[];
  assets: Asset[];
  contacts: { id: string; phone: string; email: string; suppressed: boolean }[];
  inbox: InboxItem[];
  dns: { assetId: string; records: DNSAssessment; checkedAt: string }[];
  ramps: {
    assetId: string;
    enabled: boolean;
    startedAt: string;
    dailyLimit: number;
    usedToday: number;
  }[];
  metrics: {
    total: number;
    accepted: number;
    confirmed: number;
    unknown: number;
    filtered: number;
    window: string;
    source: string;
  };
  limits: string[];
  observedAt: string;
};
export type JobDetail = {
  job: DeliveryJob;
  events: {
    id: string;
    kind: string;
    source: string;
    providerCode: string | null;
    occurredAt: string;
    receivedAt: string;
  }[];
  observedAt: string;
};
export type InboxDetail = {
  body: string;
  subject: string;
  contactId: string;
  channel: string;
  state: string;
  version: number;
};
