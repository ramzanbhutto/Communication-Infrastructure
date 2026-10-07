import { useCallback, useEffect, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import {
  ArrowRight,
  GitBranch,
  MailCheck,
  ChevronRight,
  FlaskConical,
  LayoutDashboard,
  ListFilter,
  MessagesSquare,
  Radio,
  RefreshCw,
  RotateCcw,
  ShieldCheck
} from 'lucide-react';
import { ApiError, message, post } from './api/client';
import { useRead } from './api/use-read';
import type { User } from './api/types';
import { Assets, AssetInvestigation } from './components/assets';
import { Conversations } from './components/conversations';
import { Decisions, DecisionInspector } from './components/decisions';
import { Desk } from './components/desk';
import { Infrastructure } from './components/infrastructure';
import { Campaigns } from './components/campaigns';
import { EmailDiagnosticsPage } from './components/email-diagnostics';
import { Badge, Loading, Notice, Stamp } from './components/ui';

const pages = {
  campaigns: {
    name: 'Campaigns',
    heading: 'Campaign operations',
    subtitle: 'Follow scheduled work, recorded decisions and verified delivery.',
    icon: GitBranch
  },
  email: {
    name: 'Email diagnostics',
    heading: 'Email diagnostics',
    subtitle: 'Published configuration, lookup evidence and affected campaign work.',
    icon: MailCheck
  },
  infrastructure: {
    name: 'Infrastructure',
    heading: 'Communication infrastructure',
    subtitle: 'Follow provider requests, verified outcomes and inbound events.',
    icon: ShieldCheck
  },
  desk: {
    name: 'Desk',
    heading: 'Operations desk',
    subtitle: 'Review sending issues, policy decisions and reply processing.',
    icon: LayoutDashboard
  },
  assets: {
    name: 'Sending assets',
    heading: 'Sending assets',
    subtitle: 'Investigate the assets behind each outreach decision.',
    icon: Radio
  },
  decisions: {
    name: 'Decisions',
    heading: 'Decision explorer',
    subtitle: 'Recorded facts, readable reasons and a clear path to investigation.',
    icon: ListFilter
  },
  conversations: {
    name: 'Conversations',
    heading: 'Conversations',
    subtitle: 'Turn replies into human-assisted action with explicit eligibility checks.',
    icon: MessagesSquare
  }
};
type Page = keyof typeof pages;
function currentPage(): Page {
  const hash = location.hash.slice(1);
  return hash in pages ? (hash as Page) : 'desk';
}

export default function App() {
  const [revision, setRevision] = useState(0);
  const [sessionRevision, setSessionRevision] = useState(0);
  const [page, setPage] = useState<Page>(currentPage);
  const [decisionOutcome, setDecisionOutcome] = useState<'' | 'blocked'>('');
  const [assetId, setAssetId] = useState<string | null>(null);
  const [decisionId, setDecisionId] = useState<string | null>(null);
  const [contactId, setContactId] = useState<string | null>(null);
  const [lastFresh, setLastFresh] = useState<Date | null>(null);
  const [authPending, setAuthPending] = useState(false);
  const [authError, setAuthError] = useState<string | null>(null);
  const [mutationBusy, setMutationBusy] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [confirmation, setConfirmation] = useState('');
  const [discardUnconfirmed, setDiscardUnconfirmed] = useState(false);
  const [resetPending, setResetPending] = useState(false);
  const [resetError, setResetError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const session = useRead<{ user: User; demo: true }>('/session', sessionRevision);
  useEffect(() => {
    const handle = () => {
      setPage(currentPage());
      setLastFresh(null);
    };
    window.addEventListener('hashchange', handle);
    return () => window.removeEventListener('hashchange', handle);
  }, []);
  const refresh = useCallback(() => setRevision((value) => value + 1), []);
  const fresh = useCallback(
    (date: Date) => setLastFresh((previous) => (previous && previous > date ? previous : date)),
    []
  );
  const navigate = (next: Page, outcome: '' | 'blocked' = '') => {
    if (!mutationBusy) {
      if (next === 'decisions') setDecisionOutcome(outcome);
      setPage(next);
      setLastFresh(null);
      location.hash = next;
    }
  };
  const inspectAsset = (id: string) => {
    setDecisionId(null);
    setAssetId(id);
  };
  const inspectDecision = (id: string) => {
    setAssetId(null);
    setDecisionId(id);
  };
  const openContact = (id: string) => {
    setDecisionId(null);
    setContactId(id);
    navigate('conversations');
  };
  const chooseAccount = async (userId: string) => {
    setAuthPending(true);
    setAuthError(null);
    try {
      await post('/session', { userId });
      setSessionRevision((value) => value + 1);
      refresh();
      setAssetId(null);
      setDecisionId(null);
    } catch (error) {
      setAuthError(message(error));
    } finally {
      setAuthPending(false);
    }
  };
  const reset = async () => {
    setResetPending(true);
    setResetError(null);
    try {
      await post('/demo/reset', { confirmation, discardUnconfirmed });
      setResetOpen(false);
      setConfirmation('');
      setDiscardUnconfirmed(false);
      setAssetId(null);
      setDecisionId(null);
      setContactId('c101');
      setNotice(
        'The synthetic workspace was reset. The line and consent scenarios are ready. The reply consumer is paused.'
      );
      refresh();
    } catch (error) {
      setResetError(message(error));
    } finally {
      setResetPending(false);
    }
  };
  const unauthenticated = session.error instanceof ApiError && session.error.status === 401;
  if (!session.data)
    return (
      <div className="sign-in">
        <div className="sign-in-card">
          <div className="wordmark">
            Covent<span>Ops</span>
          </div>
          <Badge tone="demo">SYNTHETIC DEMO</Badge>
          {!session.error ? (
            <Loading text="Connecting to the local workspace…" />
          ) : (
            <>
              <h1>Evidence before action.</h1>
              <p>
                An outreach operations desk with real persistence, recorded policy decisions and
                simulated providers.
              </p>
              {!unauthenticated && <Notice error>{session.error.message}</Notice>}
              {authError && <Notice error>{authError}</Notice>}
              <div className="buttons">
                <button
                  className="button primary"
                  disabled={authPending}
                  onClick={() => void chooseAccount('demo-operator')}
                >
                  <span className="signin-role">
                    Enter as operator
                    <small>Investigate, quarantine and run simulated workflows</small>
                  </span>
                  <ArrowRight size={17} />
                </button>
                <button
                  className="button"
                  disabled={authPending}
                  onClick={() => void chooseAccount('demo-viewer')}
                >
                  <span className="signin-role">
                    Explore as reviewer<small>Read-only investigation and masked exports</small>
                  </span>
                  <ArrowRight size={17} />
                </button>
              </div>
              <div className="data-note">
                <ShieldCheck size={15} />
                All contacts and provider events are synthetic. These published demo accounts are
                not a production identity system. No real outreach can be sent.
              </div>
            </>
          )}
        </div>
      </div>
    );
  const operator = session.data.user.role === 'operator';
  return (
    <>
      <a
        className="skip"
        href="#main"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById('main')?.focus();
        }}
      >
        Skip to workspace
      </a>
      <div className="shell">
        <aside className="rail">
          <div className="wordmark">
            Covent<span>Ops</span>
          </div>
          <div className="rail-caption">OUTREACH OPERATIONS</div>
          <div className="workspace-tile">
            <span className="workspace-icon">
              <FlaskConical size={18} aria-hidden="true" />
            </span>
            <div>
              Demo workspace<small>Synthetic data only</small>
            </div>
          </div>
          <nav className="nav" aria-label="Main navigation">
            {(Object.entries(pages) as [Page, (typeof pages)[Page]][]).map(([key, item]) => (
              <button
                key={key}
                className={page === key ? 'active' : ''}
                aria-current={page === key ? 'page' : undefined}
                disabled={mutationBusy}
                onClick={() => navigate(key)}
              >
                <item.icon size={18} />
                <span className="nav-label">{item.name}</span>
                <ChevronRight className="nav-arrow" size={14} aria-hidden="true" />
              </button>
            ))}
          </nav>
          <div className="rail-bottom">
            <Badge tone="demo">
              <FlaskConical size={11} />
              SYNTHETIC DEMO
            </Badge>
            <p>
              Local providers. Recorded evidence.
              <br />
              No real outreach.
            </p>
            <button
              className="button"
              disabled={!operator || mutationBusy}
              onClick={() => {
                setConfirmation('');
                setResetError(null);
                setDiscardUnconfirmed(false);
                setResetOpen(true);
              }}
            >
              <RotateCcw size={13} />
              Reset scenario
            </button>
          </div>
        </aside>
        <div className="workspace">
          <header className="topbar">
            <div className="breadcrumb">
              <span>Workspace</span>
              <ChevronRight size={12} />
              <span>Synthetic operations</span>
            </div>
            <div className="top-actions">
              <Badge tone="demo">DEMO</Badge>
              <div className="account">
                <span className="avatar">{operator ? 'DO' : 'DR'}</span>
                <select
                  aria-label="Demo account"
                  value={session.data.user.id}
                  disabled={authPending || mutationBusy}
                  onChange={(event) => void chooseAccount(event.target.value)}
                >
                  <option value="demo-operator">Demo operator</option>
                  <option value="demo-viewer">Demo reviewer</option>
                </select>
              </div>
            </div>
          </header>
          <main className="content" id="main" tabIndex={-1}>
            <div className="page-heading">
              <div>
                <div className="page-kicker">OUTREACH / LOCAL DEMO</div>
                <h1>{pages[page].heading}</h1>
                <p>{pages[page].subtitle}</p>
              </div>
              <div className="heading-actions">
                <span className="refresh-status">
                  Last successful update
                  <br />
                  <Stamp value={lastFresh} />
                </span>
                <button className="button" onClick={refresh} disabled={mutationBusy}>
                  <RefreshCw size={14} />
                  Refresh
                </button>
                <button
                  className="button icon-button"
                  aria-label="Reset synthetic scenarios"
                  title="Reset synthetic scenarios"
                  disabled={!operator || mutationBusy}
                  onClick={() => {
                    setConfirmation('');
                    setResetError(null);
                    setDiscardUnconfirmed(false);
                    setResetOpen(true);
                  }}
                >
                  <RotateCcw size={14} />
                </button>
              </div>
            </div>
            {authError && <Notice error>{authError}</Notice>}
            {notice && <Notice onClose={() => setNotice(null)}>{notice}</Notice>}
            {session.error && (
              <Notice error>
                {session.error.message} Choose a demo account again if the session expired.
              </Notice>
            )}
            <div className="page-view" key={page}>
              {page === 'campaigns' && (
                <Campaigns
                  revision={revision}
                  operator={operator}
                  changed={refresh}
                  fresh={fresh}
                  busy={setMutationBusy}
                  asset={inspectAsset}
                  decision={inspectDecision}
                  email={() => navigate('email')}
                />
              )}
              {page === 'email' && (
                <EmailDiagnosticsPage
                  revision={revision}
                  operator={operator}
                  changed={refresh}
                  fresh={fresh}
                  busy={setMutationBusy}
                  asset={inspectAsset}
                  campaigns={() => navigate('campaigns')}
                />
              )}
              {page === 'infrastructure' && (
                <Infrastructure
                  revision={revision}
                  operator={operator}
                  changed={refresh}
                  fresh={fresh}
                  busy={setMutationBusy}
                  asset={inspectAsset}
                  decision={inspectDecision}
                />
              )}
              {page === 'desk' && (
                <Desk
                  revision={revision}
                  inspectAsset={inspectAsset}
                  inspectDecision={inspectDecision}
                  conversations={() => navigate('conversations')}
                  decisions={(outcome) => navigate('decisions', outcome)}
                  fresh={fresh}
                />
              )}
              {page === 'assets' && (
                <Assets revision={revision} inspect={inspectAsset} fresh={fresh} />
              )}
              {page === 'decisions' && (
                <Decisions
                  revision={revision}
                  inspect={inspectDecision}
                  fresh={fresh}
                  initialOutcome={decisionOutcome}
                />
              )}
              {page === 'conversations' && (
                <Conversations
                  revision={revision}
                  operator={operator}
                  initialContact={contactId}
                  changed={refresh}
                  decision={inspectDecision}
                  asset={inspectAsset}
                  fresh={fresh}
                  busy={setMutationBusy}
                />
              )}
            </div>
          </main>
        </div>
      </div>
      <AssetInvestigation
        id={assetId}
        revision={revision}
        operator={operator}
        close={() => setAssetId(null)}
        changed={refresh}
      />
      <DecisionInspector
        id={decisionId}
        revision={revision}
        close={() => setDecisionId(null)}
        asset={inspectAsset}
        contact={openContact}
      />
      <Dialog.Root
        open={resetOpen}
        onOpenChange={(open) => {
          if (!resetPending) setResetOpen(open);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="overlay" />
          <Dialog.Content className="modal">
            <Dialog.Title>Reset synthetic scenarios</Dialog.Title>
            <Dialog.Description>
              This clears demo campaigns, assessments, decisions, calls, messages and audit history
              in this isolated workspace. It creates a new Redis stream namespace.
            </Dialog.Description>
            {resetError && <Notice error>{resetError}</Notice>}
            {resetError && (
              <label className="reset-discard">
                <input
                  type="checkbox"
                  checked={discardUnconfirmed}
                  onChange={(event) => setDiscardUnconfirmed(event.target.checked)}
                  disabled={resetPending}
                />{' '}
                Also discard unresolved synthetic infrastructure jobs. This only resets the local
                demo.
              </label>
            )}
            <label className="field">
              Type RESET DEMO
              <input
                type="text"
                autoComplete="off"
                value={confirmation}
                disabled={resetPending}
                onChange={(event) => setConfirmation(event.target.value)}
              />
            </label>
            <div className="action-buttons">
              <Dialog.Close className="button" disabled={resetPending}>
                Cancel
              </Dialog.Close>
              <button
                className="button danger"
                disabled={resetPending || confirmation !== 'RESET DEMO'}
                onClick={() => void reset()}
              >
                {resetPending ? 'Resetting…' : 'Reset demo'}
              </button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
