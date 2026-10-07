import { useEffect, useState } from 'react';
import {
  ArrowRight,
  CheckCircle2,
  Database,
  History,
  MailCheck,
  RefreshCw,
  ShieldCheck
} from 'lucide-react';
import { useRead } from '../api/use-read';
import { post, message } from '../api/client';
import type {
  EmailAssessment,
  EmailCheck,
  EmailDiagnostics,
  MessageAuthentication
} from '../api/email-diagnostics';
import { Badge, Empty, Loading, Notice, Panel, Stamp, Status, TextLink } from './ui';
const names = {
  mx: 'MX routing',
  spf: 'SPF policy',
  dkim: 'DKIM public key',
  dmarc: 'DMARC policy'
};
const label = (state: string) =>
  state === 'configuration_ready'
    ? 'Configuration parsed'
    : state === 'configuration_issue'
      ? 'Configuration needs attention'
      : state.replaceAll('_', ' ');
function CheckEvidence({
  title,
  check,
  previous
}: {
  title: string;
  check: EmailCheck;
  previous?: EmailCheck;
}) {
  return (
    <article className="email-check">
      <div className="row between">
        <h3>{title}</h3>
        <Status value={check.state} />
      </div>
      <p>{check.explanation}</p>
      {previous && previous.state !== check.state && (
        <div className="evidence-change">
          <History size={12} />
          {previous.state}
          <ArrowRight size={12} />
          {check.state}
        </div>
      )}
      {check.records.map((record, i) => (
        <details key={`${record.name}-${i}`} className="dns-evidence">
          <summary>DNS evidence: {record.name}</summary>
          <div>
            <p className="meta">Lookup result: {record.state}</p>
            {record.values?.length ? (
              record.values.map((value, n) => <pre key={n}>{value}</pre>)
            ) : (
              <p className="meta">No record value was established.</p>
            )}
            {record.error && <p className="meta">{record.error}</p>}
          </div>
        </details>
      ))}
      {title === 'SPF policy' && (
        <small>
          Static lookup terms observed: {check.lookups}. Contextual evaluation may require more.
        </small>
      )}
    </article>
  );
}
function AuthenticationLab({ operator }: { operator: boolean }) {
  const [scenario, setScenario] = useState('aligned');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<MessageAuthentication | null>(null);
  const verify = async () => {
    setPending(true);
    setError(null);
    try {
      const data = await post<{ authentication: MessageAuthentication }>(
        '/email-diagnostics/message-fixture',
        { scenario }
      );
      setResult(data.authentication);
    } catch (e) {
      setError(message(e));
    } finally {
      setPending(false);
    }
  };
  return (
    <Panel
      title="Message authentication lab"
      subtitle="Actual signatures on local synthetic fixtures. No messages are sent."
    >
      <div className="panel-body stack">
        <div className="filters">
          <label className="field">
            Signed message scenario
            <select
              value={scenario}
              disabled={pending || !operator}
              onChange={(e) => setScenario(e.target.value)}
            >
              <option value="aligned">Valid signature / strict alignment</option>
              <option value="unaligned">Valid signature / different From domain</option>
              <option value="tampered">Body altered after signing</option>
            </select>
          </label>
          <button
            className="button primary"
            disabled={pending || !operator}
            onClick={() => void verify()}
          >
            {pending ? 'Verifying…' : 'Verify signed fixture'}
          </button>
        </div>
        {error && <Notice error>{error}</Notice>}
        {result ? (
          <>
            <div className="row">
              <Badge tone={result.dkim === 'pass' ? 'good' : 'blocked'}>DKIM {result.dkim}</Badge>
              <Badge tone={result.dmarc === 'pass' ? 'good' : 'blocked'}>
                DMARC {result.dmarc}
              </Badge>
              <span className="meta">Verified scenario: {result.scenario}</span>
            </div>
            <p>{result.explanation}</p>
            <dl className="fact-list">
              <div>
                <dt>From domain</dt>
                <dd>{result.fromDomain}</dd>
              </div>
              <div>
                <dt>Signing domain</dt>
                <dd>{result.signingDomain}</dd>
              </div>
              <div>
                <dt>Evidence source</dt>
                <dd>{result.source}</dd>
              </div>
            </dl>
            <details className="dns-evidence">
              <summary>Inspect signed message and verification key</summary>
              <pre>{result.rawMessage}</pre>
              <pre>{result.publicKey}</pre>
            </details>
            <div className="requirements">
              <ul>
                {result.limitations.map((l) => (
                  <li key={l}>{l}</li>
                ))}
              </ul>
            </div>
          </>
        ) : (
          <p className="meta">
            Compare a valid signed message, an unaligned signing identity and an altered body. The
            verifier supports a deliberately narrow Ed25519 profile.
          </p>
        )}
      </div>
    </Panel>
  );
}
export function EmailDiagnosticsPage({
  revision,
  operator,
  changed,
  fresh,
  busy,
  asset,
  campaigns
}: {
  revision: number;
  operator: boolean;
  changed: () => void;
  fresh: (d: Date) => void;
  busy: (b: boolean) => void;
  asset: (id: string) => void;
  campaigns: () => void;
}) {
  const read = useRead<EmailDiagnostics>('/email-diagnostics', revision, 5000);
  const [selected, setSelected] = useState('email-north');
  const [selector, setSelector] = useState('mail');
  const [mode, setMode] = useState('fixture');
  const [scenario, setScenario] = useState('healthy');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [historyId, setHistoryId] = useState<string | null>(null);
  useEffect(() => {
    if (read.lastSuccess) fresh(read.lastSuccess);
  }, [read.lastSuccess, fresh]);
  const item = read.data?.items.find((i) => i.asset.id === selected) ?? read.data?.items[0];
  const latest = item?.history[0];
  const chosen = item?.history.find((h) => h.id === historyId) ?? latest;
  const assessment = chosen?.assessment;
  const historic = !!chosen && chosen.id !== latest?.id;
  const previous = item?.history[1]?.assessment;
  const check = async (changeFixture: boolean) => {
    if (!item) return;
    setPending(true);
    busy(true);
    setError(null);
    setNotice(null);
    try {
      if (changeFixture) await post(`/email-diagnostics/${item.asset.id}/fixture`, { scenario });
      const result = await post<{ assessment: EmailAssessment }>(
        `/email-diagnostics/${item.asset.id}/check`,
        { selector, mode: changeFixture ? 'fixture' : mode }
      );
      setHistoryId(null);
      setNotice(
        `${label(result.assessment.state)}. Evidence recorded from ${result.assessment.source.replaceAll('_', ' ')}.`
      );
      changed();
    } catch (e) {
      setError(message(e));
      changed();
    } finally {
      setPending(false);
      busy(false);
    }
  };
  return (
    <div className="stack">
      <div className="campaign-intro email-intro">
        <div>
          <Badge tone="demo">
            <Database size={12} /> EVIDENCE / DNS
          </Badge>
          <h2>Configuration you can explain.</h2>
          <p>Inspect published records, compare assessments and find affected campaign work.</p>
        </div>
        <button className="button" onClick={campaigns}>
          Open campaigns
          <ArrowRight size={14} />
        </button>
      </div>
      {error && <Notice error>{error}</Notice>}
      {notice && <Notice onClose={() => setNotice(null)}>{notice}</Notice>}
      {read.error && (
        <Notice error>
          {read.error.message}
          {read.data ? ' The last successful response remains visible.' : ''}
        </Notice>
      )}
      {!read.data ? (
        <Loading />
      ) : !read.data.items.length ? (
        <Panel>
          <Empty title="No email assets">
            Email diagnostics require an existing sending asset.
          </Empty>
        </Panel>
      ) : (
        <>
          <div className="email-domain-tabs">
            {read.data.items.map((i) => (
              <button
                className={`email-domain-tab ${item?.asset.id === i.asset.id ? 'selected' : ''}`}
                key={i.asset.id}
                aria-pressed={item?.asset.id === i.asset.id}
                onClick={() => {
                  setSelected(i.asset.id);
                  setHistoryId(null);
                  setMode('fixture');
                  setScenario(i.asset.id === 'email-east' ? 'broken' : 'healthy');
                  setError(null);
                  setNotice(null);
                }}
              >
                <MailCheck size={21} />
                <div>
                  <strong>{i.asset.name}</strong>
                  <small>{i.asset.address}</small>
                </div>
                <Badge
                  tone={
                    i.history[0]?.assessment.state === 'configuration_ready' ? 'good' : 'warning'
                  }
                >
                  {i.history[0] ? label(i.history[0].assessment.state) : 'Not inspected'}
                </Badge>
              </button>
            ))}
          </div>
          {item && (
            <div className="email-workspace">
              <div className="stack">
                <Panel
                  title="Recorded configuration"
                  subtitle={
                    assessment
                      ? `${assessment.domain} · selector ${assessment.selector}`
                      : 'Run diagnostics to establish evidence.'
                  }
                  action={
                    assessment && (
                      <Badge tone={assessment.state === 'configuration_ready' ? 'good' : 'warning'}>
                        {label(assessment.state)}
                      </Badge>
                    )
                  }
                >
                  <div className="panel-body stack">
                    {historic && (
                      <Notice warning>
                        Historical assessment. Campaign gating uses the latest current assessment.
                      </Notice>
                    )}
                    {assessment ? (
                      <>
                        <div className="email-evidence-summary">
                          <span>
                            <Database size={13} />
                            {assessment.source.replaceAll('_', ' ')}
                          </span>
                          <span>
                            <Stamp value={chosen?.checkedAt ?? null} />
                          </span>
                          <span>Explicit policy only</span>
                        </div>
                        <div className="email-check-grid">
                          {(Object.keys(names) as (keyof typeof names)[]).map((key) => (
                            <CheckEvidence
                              key={key}
                              title={names[key]}
                              check={assessment[key]}
                              previous={!historic ? previous?.[key] : undefined}
                            />
                          ))}
                        </div>
                        <div className="requirements">
                          <strong>What this establishes</strong>
                          <p>
                            Configuration analysis and recorded DNS evidence. Sender authentication,
                            reputation and inbox placement require separate evidence.
                          </p>
                          <ul>
                            {assessment.limitations.map((l) => (
                              <li key={l}>{l}</li>
                            ))}
                          </ul>
                        </div>
                      </>
                    ) : (
                      <Empty title="No recorded assessment">
                        Run the local fixture check to establish a baseline.
                      </Empty>
                    )}
                  </div>
                </Panel>
                <AuthenticationLab operator={operator} />
                <Panel
                  title="Delivery evidence"
                  subtitle="Last 7 days. Counts come from stored jobs and authenticated local provider events."
                >
                  <div className="email-evidence-metrics">
                    <div>
                      <strong>{item.metrics.queued}</strong>
                      <span>Jobs created</span>
                    </div>
                    <div>
                      <strong>{item.metrics.delivered}</strong>
                      <span>Confirmed deliveries</span>
                    </div>
                    <div>
                      <strong>{item.metrics.adverseJobs}</strong>
                      <span>Jobs with bounce / complaint</span>
                    </div>
                  </div>
                  <div className="panel-body">
                    <p className="meta">
                      {item.metrics.source}. These are synthetic outcomes, not measured live
                      deliverability.
                    </p>
                  </div>
                </Panel>
              </div>
              <aside className="stack">
                <Panel title="Run diagnostics" subtitle="Choose the evidence source explicitly.">
                  <div className="panel-body stack">
                    <Badge tone={item.readyForCampaigns ? 'good' : 'warning'}>
                      {item.readyForCampaigns
                        ? 'Campaign configuration gate passed'
                        : 'Campaign configuration gate held'}
                    </Badge>
                    <p className="meta">
                      Current evidence must be under 24 hours old. Fixture changes require a new
                      check.
                    </p>
                    <label className="field">
                      DNS source
                      <select
                        value={mode}
                        disabled={!operator || pending}
                        onChange={(e) => setMode(e.target.value)}
                      >
                        <option value="fixture">Synthetic DNS fixtures</option>
                        <option value="native" disabled={!item.nativeEnabled}>
                          Native DNS{' '}
                          {item.nativeEnabled ? '(configured target)' : '(not configured)'}
                        </option>
                      </select>
                    </label>
                    {mode === 'native' && (
                      <Notice warning>
                        Queries public DNS for the server-configured target: {item.nativeTarget}. No
                        messages are sent.
                      </Notice>
                    )}
                    <label className="field">
                      DKIM selector
                      <input
                        type="text"
                        maxLength={63}
                        value={selector}
                        disabled={!operator || pending}
                        onChange={(e) => setSelector(e.target.value)}
                      />
                    </label>
                    <button
                      className="button primary"
                      disabled={!operator || pending || !selector}
                      onClick={() => void check(false)}
                    >
                      <RefreshCw size={14} />
                      {pending ? 'Checking…' : 'Run diagnostics'}
                    </button>
                    {!operator && <p className="meta">Reviewer access is read-only.</p>}
                    <p className="meta">
                      Failures are recorded as unavailable. There is no automatic switch to
                      synthetic DNS.
                    </p>
                  </div>
                </Panel>
                <Panel
                  title="Synthetic DNS scenario"
                  subtitle="Change fixture records and run the same diagnostic code."
                >
                  <div className="panel-body stack">
                    <label className="field">
                      DNS scenario
                      <select
                        value={scenario}
                        disabled={!operator || pending}
                        onChange={(e) => setScenario(e.target.value)}
                      >
                        <option value="healthy">Valid explicit configuration</option>
                        <option value="broken">Revoked DKIM / invalid DMARC</option>
                        <option value="timeout">Resolver timeout</option>
                        <option value="conflicting">Conflicting SPF policies</option>
                      </select>
                    </label>
                    <button
                      className="button"
                      disabled={!operator || pending}
                      onClick={() => void check(true)}
                    >
                      Apply fixture and inspect
                    </button>
                  </div>
                </Panel>
                <Panel title="Connected work">
                  <div className="panel-body stack">
                    <div className="row">
                      <ShieldCheck size={19} />
                      <div>
                        <strong>{item.affectedCampaigns} active or paused campaigns</strong>
                        <p className="meta">Using this sending asset</p>
                      </div>
                    </div>
                    <TextLink onClick={campaigns}>Inspect campaigns</TextLink>
                    <TextLink onClick={() => asset(item.asset.id)}>Inspect sending asset</TextLink>
                  </div>
                </Panel>
                <Panel
                  title="Assessment history"
                  subtitle="Latest 10 checks; timestamps are successful persistence times."
                >
                  <div className="panel-body email-history">
                    {item.history.map((h) => (
                      <button
                        key={h.id}
                        aria-pressed={chosen?.id === h.id}
                        onClick={() => setHistoryId(h.id)}
                      >
                        <span className="row">
                          <CheckCircle2 size={14} />
                          {label(h.assessment.state)}
                        </span>
                        <small>
                          <Stamp value={h.checkedAt} /> ·{' '}
                          {h.assessment.source === 'native_dns' ? 'Native DNS' : 'Fixture'}
                        </small>
                      </button>
                    ))}
                    {!item.history.length && <p className="meta">No checks recorded.</p>}
                  </div>
                </Panel>
              </aside>
            </div>
          )}
        </>
      )}
    </div>
  );
}
