import { useEffect, useState } from 'react';
import { ArrowUpRight, ChevronLeft, ChevronRight, Download } from 'lucide-react';
import { downloadDecisions, message } from '../api/client';
import { useRead } from '../api/use-read';
import type { DecisionDetail, DecisionList } from '../api/types';
import {
  Badge,
  Drawer,
  Empty,
  Loading,
  Notice,
  Panel,
  SearchField,
  Stamp,
  Status,
  TextLink
} from './ui';

export function Decisions({
  revision,
  inspect,
  fresh,
  initialOutcome = ''
}: {
  revision: number;
  inspect: (id: string) => void;
  fresh: (date: Date) => void;
  initialOutcome?: '' | 'blocked';
}) {
  const [channel, setChannel] = useState('');
  const [outcome, setOutcome] = useState<string>(initialOutcome);
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);
  const [exporting, setExporting] = useState(false);
  const [exportNotice, setExportNotice] = useState<string | null>(null);
  const [exportError, setExportError] = useState(false);
  const filters = new URLSearchParams({ channel, outcome, search });
  const query = new URLSearchParams({
    channel,
    outcome,
    search,
    page: String(page),
    pageSize: '10'
  });
  const { data, error, lastSuccess, refreshing } = useRead<DecisionList>(
    `/decisions?${query}`,
    revision,
    5000
  );
  useEffect(() => {
    if (lastSuccess) fresh(lastSuccess);
  }, [lastSuccess, fresh]);
  const exportRows = async () => {
    setExporting(true);
    setExportNotice(null);
    try {
      await downloadDecisions(filters.toString());
      setExportError(false);
      setExportNotice(
        'Exported all matching decisions from the last 24 hours. Contact values are masked. Pagination does not restrict this export.'
      );
    } catch (e) {
      setExportError(true);
      setExportNotice(message(e));
    } finally {
      setExporting(false);
    }
  };
  return (
    <>
      <div className="filters">
        <SearchField
          label="Search decisions"
          placeholder="Search reason, record or asset"
          value={search}
          onChange={(value) => {
            setSearch(value);
            setPage(1);
          }}
        />
        <select
          aria-label="Filter decision channel"
          value={channel}
          onChange={(event) => {
            setChannel(event.target.value);
            setPage(1);
          }}
        >
          <option value="">All channels</option>
          <option value="email">Email</option>
          <option value="call">Calling</option>
          <option value="sms">SMS</option>
          <option value="imessage">iMessage</option>
        </select>
        <select
          aria-label="Filter decision outcome"
          value={outcome}
          onChange={(event) => {
            setOutcome(event.target.value);
            setPage(1);
          }}
        >
          <option value="">All outcomes</option>
          <option value="allowed">Allowed</option>
          <option value="blocked">Blocked</option>
          <option value="deferred">Deferred</option>
        </select>
        <button
          className="button"
          onClick={() => void exportRows()}
          disabled={exporting || !data || Boolean(error) || refreshing}
        >
          <Download size={15} />
          {exporting ? 'Creating export…' : 'Export filtered'}
        </button>
      </div>
      {exportNotice && (
        <Notice error={exportError} onClose={() => setExportNotice(null)}>
          {exportNotice}
        </Notice>
      )}
      {error && (
        <Notice error>
          {error.message}
          {data && ' Showing the last successful snapshot.'}
        </Notice>
      )}
      <Panel
        title="Recorded decisions"
        subtitle="Last 24 hours. Inspect immutable evidence rather than assuming current eligibility."
        action={<Badge>{data?.total ?? '…'} matching</Badge>}
      >
        {!data ? (
          !error && <Loading />
        ) : !data.items.length ? (
          <Empty title="No decisions match">
            Change the filters or make a simulated outreach attempt to record a new decision.
          </Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th>Recorded</th>
                  <th>Contact</th>
                  <th>Channel</th>
                  <th>Outcome and reason</th>
                  <th>Asset</th>
                  <th>Evidence</th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((decision) => (
                  <tr key={decision.id}>
                    <td>
                      <Stamp value={decision.recordedAt} />
                      <div className="cell-meta mono">{decision.id.slice(0, 16)}</div>
                    </td>
                    <td>
                      <div className="cell-title">{decision.contactLabel}</div>
                      <div className="cell-meta">{decision.evidence.maskedPhone}</div>
                    </td>
                    <td>
                      {decision.channel === 'call'
                        ? 'Call'
                        : decision.channel === 'email'
                          ? 'Email'
                          : decision.channel === 'imessage'
                            ? 'iMessage'
                            : 'SMS'}
                    </td>
                    <td>
                      <Status value={decision.outcome} />
                      <div className="cell-meta mono">{decision.reason}</div>
                    </td>
                    <td>{decision.assetName ?? 'Not recorded'}</td>
                    <td>
                      <button className="button" onClick={() => inspect(decision.id)}>
                        Inspect
                        <ArrowUpRight size={13} />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {data && (
          <div className="pagination">
            <span>
              {data.total} matching decisions · Page {data.page} of{' '}
              {Math.max(1, Math.ceil(data.total / data.pageSize))}
            </span>
            <div className="row">
              <button
                className="button icon-button"
                aria-label="Previous decision page"
                onClick={() => setPage((value) => value - 1)}
                disabled={page <= 1 || refreshing}
              >
                <ChevronLeft size={15} />
              </button>
              <button
                className="button icon-button"
                aria-label="Next decision page"
                onClick={() => setPage((value) => value + 1)}
                disabled={page * data.pageSize >= data.total || refreshing}
              >
                <ChevronRight size={15} />
              </button>
            </div>
          </div>
        )}
      </Panel>
      <p className="table-footnote">
        Export includes every filtered row in the last 24 hours, up to 500 records. Phone numbers
        and email addresses remain masked.
      </p>
    </>
  );
}

export function DecisionInspector({
  id,
  revision,
  close,
  asset,
  contact
}: {
  id: string | null;
  revision: number;
  close: () => void;
  asset: (id: string) => void;
  contact: (id: string) => void;
}) {
  const { data, error } = useRead<DecisionDetail>(
    id ? `/decisions/${encodeURIComponent(id)}` : null,
    revision
  );
  const d = data?.decision;
  const e = d?.evidence;
  return (
    <Drawer
      open={Boolean(id)}
      onClose={close}
      title="Decision inspector"
      subtitle={
        d
          ? `${d.channel === 'call' ? 'Call' : d.channel === 'email' ? 'Email' : d.channel === 'imessage' ? 'iMessage' : 'SMS'} · ${d.contactLabel} · Recorded evidence`
          : 'Loading the recorded eligibility decision'
      }
      eyebrow="Policy evidence"
    >
      {error && <Notice error>{error.message}</Notice>}
      {!d || !e ? (
        !error && <Loading />
      ) : (
        <>
          <div className="row between">
            <Status value={d.outcome} />
            <span className="mono">{d.reason}</span>
          </div>
          <section className="drawer-section">
            <h3>Derived explanation</h3>
            <div className="explanation">
              {data!.explanation || 'No explanation is available for this reason code.'}
            </div>
            <p className="meta">{data!.explanationSource}</p>
          </section>
          <section className="drawer-section">
            <h3>Recorded facts</h3>
            <dl className="fact-list">
              {d.channel === 'imessage' && (
                <div>
                  <dt>Explicit iMessage permission</dt>
                  <dd>
                    {e.imessagePermissionRecorded ? 'Recorded' : 'Not recorded'}
                    <br />
                    <Stamp value={e.imessagePermissionAt ?? null} />
                    <br />
                    {e.imessagePermissionSource ?? 'No iMessage permission source recorded'}
                  </dd>
                </div>
              )}
              {d.channel === 'email' && (
                <div>
                  <dt>Explicit email permission</dt>
                  <dd>
                    {e.emailPermissionRecorded ? 'Recorded' : 'Not recorded'}
                    <br />
                    <Stamp value={e.emailPermissionAt ?? null} />
                    <br />
                    {e.emailPermissionSource ?? 'No email permission source recorded'}
                  </dd>
                </div>
              )}
              <div>
                <dt>Decision timestamp</dt>
                <dd>
                  <Stamp value={d.recordedAt} />
                </dd>
              </div>
              <div>
                <dt>Evidence timestamp</dt>
                <dd>
                  <Stamp value={e.checkedAt} />
                </dd>
              </div>
              <div>
                <dt>Contact</dt>
                <dd>
                  {e.contactLabel}
                  <br />
                  {e.maskedPhone}
                  <br />
                  {e.maskedEmail}
                </dd>
              </div>
              <div>
                <dt>DNC suppression</dt>
                <dd>{e.dnc ? 'Listed' : 'Not listed in demo records'}</dd>
              </div>
              <div>
                <dt>Opt-out</dt>
                <dd>
                  <Stamp value={e.optedOutAt} />
                </dd>
              </div>
              <div>
                <dt>Explicit SMS consent</dt>
                <dd>
                  <Stamp value={e.smsConsentAt} />
                  <br />
                  {e.consentSource ?? 'No consent source recorded'}
                </dd>
              </div>
              <div>
                <dt>Warm signal</dt>
                <dd>
                  {e.warmSignal?.replaceAll('_', ' ') ?? 'Not recorded'}
                  <br />
                  <Stamp value={e.warmAt} />
                </dd>
              </div>
              <div>
                <dt>Email open</dt>
                <dd>
                  <Stamp value={e.emailOpenedAt} />
                  <br />
                  <span className="meta">Does not grant SMS permission</span>
                </dd>
              </div>
              <div>
                <dt>Asset at decision time</dt>
                <dd>
                  {d.assetName ?? 'Not recorded'}
                  <br />
                  {e.assetStatus} · Version {e.assetVersion}
                </dd>
              </div>
              <div>
                <dt>Evidence source</dt>
                <dd>{e.source.replaceAll('_', ' ')}</dd>
              </div>
            </dl>
          </section>
          <section className="drawer-section">
            <h3>Unavailable evidence</h3>
            <div className="unavailable">
              {e.unavailable.length
                ? e.unavailable.join('. ') + '.'
                : 'No unavailable fields were recorded.'}{' '}
              This demo makes no claim of legal compliance or live provider verification.
            </div>
          </section>
          <section className="drawer-section">
            <h3>Related records</h3>
            {d.assetId && (
              <TextLink onClick={() => asset(d.assetId!)}>Inspect current asset</TextLink>
            )}
            <TextLink onClick={() => contact(d.contactId)}>Open contact workflow</TextLink>
            <p className="meta">
              Related views show current state. They do not change this historical evidence
              snapshot.
            </p>
          </section>
        </>
      )}
    </Drawer>
  );
}
