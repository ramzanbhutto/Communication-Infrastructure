export type User = { id: string; workspaceId: string; name: string; role: 'operator' | 'viewer' };
export type Sample = {
  id: number;
  attempts: number;
  filtered: number;
  spamLabel: boolean;
  observedAt: string;
  source: string;
};
export type Asset = {
  id: string;
  kind: 'phone' | 'email' | 'imessage';
  name: string;
  address: string;
  status: 'active' | 'quarantined';
  version: number;
  quarantinedAt: string | null;
  quarantineReason: string | null;
  sample: Sample | null;
  emailConfig: {
    spf: boolean;
    dkim: boolean;
    dmarc: string;
    warmupDay: number;
    source: string;
  } | null;
};
export type Audit = {
  id: number;
  actorName: string;
  action: string;
  assetId: string | null;
  recordId: string | null;
  reason: string | null;
  recordedAt: string;
};
export type ContactSummary = { id: string; label: string; phone: string; email: string };
export type Contact = {
  id: string;
  name: string;
  phone: string;
  email: string;
  dnc: boolean;
  optedOutAt: string | null;
  smsConsentAt: string | null;
  consentSource: string | null;
  warmSignal: string | null;
  warmAt: string | null;
  emailOpenedAt: string | null;
};
export type ContactDetail = {
  contact: Contact;
  eligibility: Record<string, { sms: string; call: string }>;
  source: string;
};
export type Evidence = {
  emailPermissionRecorded?: boolean;
  imessagePermissionRecorded?: boolean;
  imessagePermissionAt?: string;
  imessagePermissionSource?: string;
  emailPermissionAt?: string;
  emailPermissionSource?: string;
  contactLabel: string;
  maskedPhone: string;
  maskedEmail: string;
  dnc: boolean;
  optedOutAt: string | null;
  smsConsentAt: string | null;
  consentSource: string | null;
  warmSignal: string | null;
  warmAt: string | null;
  emailOpenedAt: string | null;
  assetStatus: string;
  assetVersion: number;
  sample: Sample | null;
  checkedAt: string;
  source: string;
  unavailable: string[];
};
export type Decision = {
  id: string;
  contactId: string;
  contactLabel: string;
  assetId: string | null;
  assetName: string | null;
  channel: 'sms' | 'call' | 'email' | 'imessage';
  outcome: 'allowed' | 'blocked' | 'deferred';
  reason: string;
  evidence: Evidence;
  recordedAt: string;
};
export type DecisionList = {
  items: Decision[];
  total: number;
  page: number;
  pageSize: number;
  window: string;
  observedAt: string;
};
export type DecisionDetail = { decision: Decision; explanation: string; explanationSource: string };
export type AssetDetail = {
  asset: Asset;
  samples: Sample[];
  audit: Audit[];
  restoreProblems: string[];
  observedAt: string;
};
export type Queue = {
  state: 'ready' | 'paused' | 'unavailable' | 'initializing';
  pending: number | null;
  unpublished: number;
  paused: boolean;
  error: string | null;
  checkedAt: string;
};
export type Call = {
  id: string;
  contactId: string;
  assetId: string;
  decisionId: string;
  status: 'active' | 'completed';
  outcome: string | null;
  startedAt: string;
  completedAt: string | null;
};
export type Reply = {
  id: string;
  contactId: string;
  contactLabel: string;
  assetId: string;
  body: string;
  tag: 'interested' | 'opt-out' | 'question';
  receivedAt: string;
  persistedAt: string;
};
export type Conversations = {
  replies: Reply[];
  calls: Call[];
  assets: Asset[];
  queue: Queue;
  observedAt: string;
  demo: true;
};
export type Issue = {
  id: string;
  kind: 'asset' | 'decision' | 'queue';
  title: string;
  detail: string;
  lane: string;
  assetId?: string;
  decisionId?: string;
  contactId?: string;
  source: string;
  severity: string;
};
export type Desk = {
  assets: Asset[];
  featuredHistory: Sample[];
  issues: Issue[];
  decisions: Decision[];
  activity: Audit[];
  queue: Queue;
  metrics: { checked: number; blocked: number; delivered: number; window: string; source: string };
  services: { name: string; state: string; source: string; lastSuccessfulTick?: number }[];
  generation: number;
  resetAt: string;
  observedAt: string;
  demo: true;
};
export type OutreachResult = {
  id: string;
  decisionId: string;
  status: 'active' | 'delivered';
  simulated: true;
};
