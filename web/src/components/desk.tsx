import { useEffect, useState } from 'react';
import {
  ArrowUpRight,
  Database,
  GitBranch,
  Radio,
  Send,
  ShieldCheck,
  Workflow
} from 'lucide-react';
import { useRead } from '../api/use-read';
import type { Desk as DeskData, Issue } from '../api/types';
import { AuditTrail, Badge, Empty, Loading, Notice, Panel, SampleChart, Status, Stamp } from './ui';

export function Desk({
  revision,
  inspectAsset,
  inspectDecision,
  conversations,
  decisions,
  fresh
}: {
  revision: number;
  inspectAsset: (id: string) => void;
  inspectDecision: (id: string) => void;
  conversations: () => void;
  decisions: (outcome?: '' | 'blocked') => void;
  fresh: (date: Date) => void;
}) {
  const [issueKind, setIssueKind] = useState<'all' | Issue['kind']>('all');
  const { data, error, lastSuccess } = useRead<DeskData>('/desk', revision, 5000);
  useEffect(() => {
    if (lastSuccess) fresh(lastSuccess);
  }, [lastSuccess, fresh]);
  const selected = data?.assets.find((asset) => asset.id === 'line-delta');
  const preview = selected?.sample;
  const visibleIssues =
    data?.issues.filter((issue) => issueKind === 'all' || issue.kind === issueKind) ?? [];
  return (
    <>
      {error && (
        <Notice error>
          {error.message}
          {data && ' Showing the last successful snapshot.'}
        </Notice>
      )}
      {!data ? (
        !error && <Loading />
      ) : (
        <>
          <div className="metrics">
            <button
              className="metric"
              onClick={() => decisions()}
              aria-label="View recorded policy decisions"
            >
              <div className="metric-label">
                Policy decisions
                <span className="metric-icon">
                  <GitBranch size={18} aria-hidden="true" />
                </span>
              </div>
              <strong>{data.metrics.checked}</strong>
              <span className="meta">Recorded checks · last 24 hours</span>
              <span className="metric-link">
                Explore decisions
                <ArrowUpRight size={14} aria-hidden="true" />
              </span>
            </button>
            <button
              className="metric"
              onClick={() => decisions('blocked')}
              aria-label="View blocked outreach decisions"
            >
              <div className="metric-label">
                Outreach blocked
                <span className="metric-icon">
                  <ShieldCheck size={18} aria-hidden="true" />
                </span>
              </div>
              <strong>{data.metrics.blocked}</strong>
              <span className="meta">Suppressed before delivery · last 24 hours</span>
              <span className="metric-link">
                Review blocked outreach
                <ArrowUpRight size={14} aria-hidden="true" />
              </span>
            </button>
            <button
              className="metric"
              onClick={conversations}
              aria-label="Open human-assisted outreach"
            >
              <div className="metric-label">
                Simulated SMS delivered
                <span className="metric-icon">
                  <Send size={18} aria-hidden="true" />
                </span>
              </div>
              <strong>{data.metrics.delivered}</strong>
              <span className="meta">Stored simulated receipts · last 24 hours</span>
              <span className="metric-link">
                Open outreach
                <ArrowUpRight size={14} aria-hidden="true" />
              </span>
            </button>
          </div>
          <div className="desk-grid">
            <Panel
              title="Attention queue"
              subtitle="What needs a decision from you"
              action={<Badge tone="warning">{data.issues.length} open issues</Badge>}
            >
              <div className="queue-filters" role="group" aria-label="Filter attention queue">
                {(
                  [
                    ['all', 'All issues'],
                    ['asset', 'Assets'],
                    ['decision', 'Policy'],
                    ['queue', 'Queue']
                  ] as const
                ).map(([kind, label]) => (
                  <button
                    type="button"
                    key={kind}
                    aria-pressed={kind === issueKind}
                    onClick={() => setIssueKind(kind)}
                  >
                    {label}
                    <span>
                      {kind === 'all'
                        ? data.issues.length
                        : data.issues.filter((issue) => issue.kind === kind).length}
                    </span>
                  </button>
                ))}
              </div>
              {!visibleIssues.length ? (
                <Empty title="No recorded issues need attention">
                  {data.issues.length
                    ? 'No issues match this category. Choose another category to see the remaining issues.'
                    : 'The current observations and queue state have no flagged issues.'}
                </Empty>
              ) : (
                <div className="issue-list">
                  {visibleIssues.map((issue) => (
                    <article
                      className={`issue issue-${issue.kind} ${issue.severity === 'blocked' ? 'issue-blocked' : ''}`}
                      key={issue.id}
                    >
                      <div className="issue-top">
                        <span className="issue-number">
                          {issue.kind === 'asset' ? (
                            <Radio size={16} aria-hidden="true" />
                          ) : issue.kind === 'decision' ? (
                            <ShieldCheck size={16} aria-hidden="true" />
                          ) : (
                            <Workflow size={16} aria-hidden="true" />
                          )}
                          {String(
                            data.issues.findIndex((item) => item.id === issue.id) + 1
                          ).padStart(2, '0')}
                        </span>
                        <Badge tone={issue.severity === 'blocked' ? 'blocked' : 'warning'}>
                          {issue.lane}
                        </Badge>
                      </div>
                      <div className="issue-content">
                        <h3>{issue.title}</h3>
                        <p>{issue.detail}</p>
                      </div>
                      <div className="issue-footer">
                        <div className="meta">{issue.source}</div>
                        <button
                          className="link-button"
                          onClick={() => {
                            if (issue.assetId) inspectAsset(issue.assetId);
                            else if (issue.decisionId) inspectDecision(issue.decisionId);
                            else conversations();
                          }}
                        >
                          {issue.kind === 'asset'
                            ? 'Inspect asset'
                            : issue.kind === 'decision'
                              ? 'Inspect decision'
                              : 'Review queue'}
                          <ArrowUpRight size={13} />
                        </button>
                      </div>
                    </article>
                  ))}
                </div>
              )}
              {selected && (
                <div className="investigation-preview">
                  <div>
                    <span className="eyebrow">Featured investigation</span>
                    <h3>{selected.name} · filter rate</h3>
                    {preview && preview.attempts > 0 ? (
                      <>
                        <div className="filter-stat">
                          <strong>
                            {((preview.filtered / preview.attempts) * 100).toFixed(1)}%
                          </strong>
                          <Status value={selected.status} />
                        </div>
                        <p className="meta">
                          {preview.filtered} filtered / {preview.attempts} observed attempts
                          <br />
                          Simulated provider sample, not live traffic
                        </p>
                      </>
                    ) : (
                      <p className="meta">A measured provider sample is unavailable.</p>
                    )}
                    <button className="link-button" onClick={() => inspectAsset(selected.id)}>
                      Open investigation
                      <ArrowUpRight size={13} />
                    </button>
                  </div>
                  <div>
                    {preview && <SampleChart samples={data.featuredHistory} />}
                    <p className="meta">
                      Latest stored observation · <Stamp value={preview?.observedAt ?? null} />
                    </p>
                  </div>
                </div>
              )}
            </Panel>
            <aside className="stack desk-aside">
              <Panel title="Service status">
                <div className="panel-body">
                  {data.services.map((service) => (
                    <div className="service" key={service.name}>
                      <div className="service-name">
                        {service.name}
                        <small>{service.source}</small>
                      </div>
                      <Status value={service.state} />
                    </div>
                  ))}
                  <p className="meta section-gap">
                    Queue entries:{' '}
                    {data.queue.pending === null ? 'unavailable' : data.queue.pending}. Unpublished
                    events: {data.queue.unpublished}.
                  </p>
                </div>
              </Panel>
              <Panel title="Recent activity">
                <div className="panel-body">
                  <AuditTrail
                    events={data.activity.slice(0, 5)}
                    onAsset={inspectAsset}
                    onDecision={inspectDecision}
                  />
                </div>
              </Panel>
            </aside>
          </div>
          <div className="data-note">
            <Database size={12} />
            {data.metrics.source}. Last snapshot: <Stamp value={data.observedAt} />.
          </div>
        </>
      )}
    </>
  );
}
