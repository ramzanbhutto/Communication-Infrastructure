import type { Asset } from './types';
import type { DNSRecord } from './infrastructure';
export type EmailCheck = {
  state: string;
  explanation: string;
  records: DNSRecord[];
  lookups: number;
};
export type EmailAssessment = {
  domain: string;
  selector: string;
  source: string;
  state: string;
  fixtureScenario?: string;
  checkedAt: string;
  mx: EmailCheck;
  spf: EmailCheck;
  dkim: EmailCheck;
  dmarc: EmailCheck;
  limitations: string[];
};
export type EmailDiagnostics = {
  items: {
    asset: Asset;
    history: { id: string; assessment: EmailAssessment; checkedAt: string }[];
    affectedCampaigns: number;
    readyForCampaigns: boolean;
    nativeEnabled: boolean;
    nativeTarget: string;
    metrics: {
      queued: number;
      delivered: number;
      adverseJobs: number;
      window: string;
      source: string;
    };
  }[];
  observedAt: string;
};

export type MessageAuthentication = {
  source: string;
  scenario: string;
  dkim: string;
  dmarc: string;
  fromDomain: string;
  signingDomain: string;
  rawMessage: string;
  publicKey: string;
  explanation: string;
  limitations: string[];
};
