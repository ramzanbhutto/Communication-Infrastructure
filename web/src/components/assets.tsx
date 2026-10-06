import { useEffect, useState, type FormEvent } from 'react';
import { ArrowUpRight, Radio, Shield, ShieldOff } from 'lucide-react';
import { useRead } from '../api/use-read';
import { ApiError, message } from '../api/client';
import { changeAsset } from '../api/assets';
import type { Asset, AssetDetail } from '../api/types';
import {
  AuditTrail,
  Badge,
  Drawer,
  Empty,
  Loading,
  Notice,
  Panel,
  SampleChart,
  SearchField,
  Stamp,
  Status
} from './ui';

export function Assets({
  revision,
  inspect,
  fresh
}: {
  revision: number;
  inspect: (id: string) => void;
  fresh: (date: Date) => void;
}) {
  const [kind, setKind] = useState('phone');
  const [status, setStatus] = useState('');
  const [search, setSearch] = useState('');
  const query = new URLSearchParams({ kind, status, search });
  const { data, error, lastSuccess } = useRead<{
    items: Asset[];
    total: number;
    observedAt: string;
  }>(`/assets?${query}`, revision, 5000);
  useEffect(() => {
    if (lastSuccess) fresh(lastSuccess);
  }, [lastSuccess, fresh]);
  return (
    <>
      <div className="filters">
        <div className="tabs" aria-label="Asset type">
          {['phone', 'email'].map((value) => (
            <button
              aria-pressed={value === kind}
              className={value === kind ? 'active' : ''}
              key={value}
              onClick={() => setKind(value)}
            >
              {value === 'phone' ? 'Phone lines' : 'Email infrastructure'}
            </button>
          ))}
        </div>
        <SearchField
          label="Search assets"
          placeholder="Search by name or address"
          value={search}
          onChange={setSearch}
        />
        <select
          aria-label="Filter asset status"
          value={status}
          onChange={(event) => setStatus(event.target.value)}
        >
          <option value="">All states</option>
          <option value="active">Active</option>
          <option value="quarantined">Quarantined</option>
        </select>
      </div>
      {error && (
        <Notice error>
          {error.message}
          {data && ' Showing the last successful snapshot.'}
        </Notice>
      )}
      <Panel
        title={kind === 'phone' ? 'Sending lines' : 'Sending domains'}
        subtitle={
          kind === 'phone'
            ? 'Measured observations with safe state transitions'
            : 'Synthetic DNS configuration and warmup visibility. No live DNS queries.'
        }
        action={<Badge>{data?.total ?? '…'} assets</Badge>}
      >
        {!data ? (
          !error && <Loading />
        ) : !data.items.length ? (
          <Empty title="No assets match">Try a different search or state filter.</Empty>
        ) : (
          <div className="table-scroll">
            <table className="table">
              <thead>
                <tr>
                  <th>Asset</th>
                  <th>State</th>
                  <th>{kind === 'phone' ? 'Latest filter rate' : 'Authentication'}</th>
                  <th>{kind === 'phone' ? 'Observation' : 'Warmup'}</th>
                  <th>
                    <span className="meta">Investigation</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {data.items.map((asset) => (
                  <tr key={asset.id}>
                    <td>
                      <div className="cell-title">{asset.name}</div>
                      <div className="cell-meta">{asset.address}</div>
                    </td>
                    <td>
                      <Status value={asset.status} />
                    </td>
                    <td>
                      {asset.kind === 'phone' ? (
                        asset.sample && asset.sample.attempts > 0 ? (
                          <>
                            <strong>
                              {((asset.sample.filtered / asset.sample.attempts) * 100).toFixed(1)}%
                            </strong>
                            <div className="cell-meta">
                              {asset.sample.filtered} / {asset.sample.attempts} filtered
                              {asset.sample.spamLabel && ' · Provider flag'}
                            </div>
                          </>
                        ) : (
                          'Unavailable'
                        )
                      ) : (
                        <>
                          <Badge tone={asset.emailConfig?.dkim ? 'good' : 'warning'}>
                            DKIM {asset.emailConfig?.dkim ? 'present' : 'missing'}
                          </Badge>
                          <div className="cell-meta">
                            DMARC: {asset.emailConfig?.dmarc ?? 'unavailable'}
                          </div>
                        </>
                      )}
                    </td>
                    <td>
                      {asset.kind === 'phone' ? (
                        <>
                          <Stamp value={asset.sample?.observedAt ?? null} />
                          <div className="cell-meta">Simulated provider</div>
                        </>
                      ) : (
                        <>
                          <strong>Day {asset.emailConfig?.warmupDay ?? 'unknown'}</strong>
                          <div className="cell-meta">Synthetic fixture</div>
                        </>
                      )}
                    </td>
                    <td>
                      <button className="button" onClick={() => inspect(asset.id)}>
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
      </Panel>
      <p className="table-footnote">
        Status comes from PostgreSQL. Filter rate is filtered attempts divided by observed attempts
        in the latest sample.
      </p>
    </>
  );
}

export function AssetInvestigation({
  id,
  revision,
  operator,
  close,
  changed
}: {
  id: string | null;
  revision: number;
  operator: boolean;
  close: () => void;
  changed: () => void;
}) {
  const [localRevision, setLocalRevision] = useState(0);
  const [reason, setReason] = useState('');
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<Error | null>(null);
  const read = useRead<AssetDetail>(
    id ? `/assets/${encodeURIComponent(id)}` : null,
    revision + localRevision
  );
  useEffect(() => {
    setReason('');
    setResult(null);
    setError(null);
  }, [id]);
  const a = read.data?.asset;
  const act = async (action: 'quarantine' | 'restore' | 'recovery') => {
    if (!a || pending) return;
    setPending(true);
    setError(null);
    setResult(null);
    try {
      const response = await changeAsset(
        a.id,
        action,
        action === 'recovery'
          ? 'Recorded a clean simulated provider observation for restoration review.'
          : reason.trim(),
        a.version
      );
      setResult(
        action === 'recovery'
          ? 'Clean observation recorded. Review the requirements before restoring.'
          : response.changed
            ? `${response.asset.name} is now ${response.asset.status}. The audit entry is committed.`
            : `The asset was already ${response.asset.status}. No duplicate audit entry was added.`
      );
      setReason('');
      setLocalRevision((value) => value + 1);
      changed();
    } catch (e) {
      setError(e instanceof Error ? e : new Error(message(e)));
      setLocalRevision((value) => value + 1);
      changed();
    } finally {
      setPending(false);
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (a) void act(a.status === 'active' ? 'quarantine' : 'restore');
  };
  return (
    <Drawer
      open={Boolean(id)}
      onClose={() => {
        if (!pending) close();
      }}
      title={a?.name ?? 'Asset investigation'}
      subtitle={
        a
          ? `${a.address} · Version ${a.version} · Synthetic sending asset`
          : 'Loading stored observations and audit records'
      }
      eyebrow="Sending asset"
    >
      {read.error && (
        <Notice error>
          {read.error.message}
          {read.data && ' The visible snapshot may be stale.'}
        </Notice>
      )}
      {!a ? (
        !read.error && <Loading />
      ) : (
        <>
          <div className="row between">
            <Status value={a.status} />
            <Badge>{a.kind === 'phone' ? 'Simulated provider' : 'Synthetic DNS fixture'}</Badge>
          </div>
          {result && <Notice>{result}</Notice>}
          {error && (
            <Notice error>
              {error.message}
              {error instanceof ApiError && Array.isArray(error.details) && (
                <ul>
                  {error.details.map((detail, index) => (
                    <li key={index}>{String(detail)}</li>
                  ))}
                </ul>
              )}
            </Notice>
          )}
          {read.refreshing && (
            <p className="meta" role="status">
              Refreshing the committed state…
            </p>
          )}
          {a.kind === 'phone' ? (
            <>
              <div className="detail-grid">
                <div className="detail-block">
                  <div className="label">Latest measured filter rate</div>
                  <div className="value big">
                    {a.sample && a.sample.attempts > 0
                      ? `${((a.sample.filtered / a.sample.attempts) * 100).toFixed(1)}%`
                      : 'Unavailable'}
                  </div>
                  <div className="meta">
                    {a.sample
                      ? `${a.sample.filtered} filtered / ${a.sample.attempts} attempts`
                      : 'No observation'}
                  </div>
                </div>
                <div className="detail-block">
                  <div className="label">Provider label</div>
                  <div className="value">
                    {a.sample
                      ? a.sample.spamLabel
                        ? 'Flagged as spam'
                        : 'No spam label'
                      : 'Unavailable'}
                  </div>
                  <div className="meta">
                    <Stamp value={a.sample?.observedAt ?? null} />
                  </div>
                </div>
              </div>
              <section className="drawer-section">
                <h3>Measured history</h3>
                <SampleChart samples={read.data!.samples} />
                <p className="meta">
                  Stored simulated observations. The dashed line marks the 5% restoration threshold.
                </p>
                <details>
                  <summary className="meta">
                    View {read.data!.samples.length} sample records
                  </summary>
                  <div className="table-scroll">
                    <table className="table">
                      <thead>
                        <tr>
                          <th>Observed</th>
                          <th>Attempts</th>
                          <th>Filtered</th>
                          <th>Spam label</th>
                        </tr>
                      </thead>
                      <tbody>
                        {read.data!.samples.map((sample) => (
                          <tr key={sample.id}>
                            <td>
                              <Stamp value={sample.observedAt} />
                            </td>
                            <td>{sample.attempts}</td>
                            <td>{sample.filtered}</td>
                            <td>{sample.spamLabel ? 'Present' : 'Absent'}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </details>
              </section>
              {a.status === 'quarantined' && (
                <section className="drawer-section">
                  <h3>Restoration requirements</h3>
                  {read.data!.restoreProblems.length ? (
                    <div className="requirements">
                      The server will reject restoration until these checks pass.
                      <ul>
                        {read.data!.restoreProblems.map((problem) => (
                          <li key={problem}>{problem}</li>
                        ))}
                      </ul>
                    </div>
                  ) : (
                    <Notice>
                      The latest stored observation meets the current restoration prerequisites. The
                      server will check again when you submit.
                    </Notice>
                  )}
                  <p className="meta">
                    Quarantined <Stamp value={a.quarantinedAt} />. Reason: {a.quarantineReason}
                  </p>
                </section>
              )}
              {operator ? (
                <form className="action-form" onSubmit={submit}>
                  <div className="row">
                    <Shield size={17} />
                    <h3>{a.status === 'active' ? 'Quarantine this line' : 'Restore this line'}</h3>
                  </div>
                  <p className="meta">
                    {a.status === 'active'
                      ? 'Quarantine prevents new calls and SMS from using this line. The reason and actor are recorded in the same transaction.'
                      : 'A new clean observation is required. An active call or a changed asset version will also prevent this transition.'}
                  </p>
                  <label className="field">
                    Explanation
                    <textarea
                      placeholder="Why is this operational change needed?"
                      required
                      minLength={3}
                      maxLength={300}
                      value={reason}
                      onChange={(event) => setReason(event.target.value)}
                      disabled={pending}
                    />
                  </label>
                  <div className="action-buttons">
                    <button
                      className={`button ${a.status === 'active' ? 'danger' : 'primary'}`}
                      disabled={
                        pending ||
                        read.refreshing ||
                        Boolean(read.error) ||
                        reason.trim().length < 3
                      }
                    >
                      {pending ? (
                        'Waiting for confirmation…'
                      ) : a.status === 'active' ? (
                        <>
                          <ShieldOff size={15} />
                          Quarantine line
                        </>
                      ) : (
                        <>
                          <Radio size={15} />
                          Restore line
                        </>
                      )}
                    </button>
                    {a.status === 'quarantined' && (
                      <button
                        type="button"
                        className="button"
                        disabled={pending || read.refreshing || Boolean(read.error)}
                        onClick={() => void act('recovery')}
                      >
                        Record clean demo sample
                      </button>
                    )}
                  </div>
                </form>
              ) : (
                <Notice warning>
                  The reviewer can inspect this record. Switch to the demo operator to change line
                  state.
                </Notice>
              )}
            </>
          ) : (
            <>
              <div className="detail-grid">
                {[
                  ['SPF', a.emailConfig?.spf ? 'Present' : 'Missing'],
                  ['DKIM', a.emailConfig?.dkim ? 'Present' : 'Missing'],
                  ['DMARC', a.emailConfig?.dmarc ?? 'Unavailable'],
                  ['Warmup', `Day ${a.emailConfig?.warmupDay ?? 'unknown'}`]
                ].map(([label, value]) => (
                  <div className="detail-block" key={label}>
                    <div className="label">{label}</div>
                    <div className="value">{value}</div>
                  </div>
                ))}
              </div>
              <Notice warning>
                Email infrastructure is visible through synthetic configuration fixtures. Live DNS
                verification and email sending are outside this demo. Quarantine actions are
                implemented for phone lines.
              </Notice>
            </>
          )}
          <section className="drawer-section">
            <h3>Audit trail</h3>
            <AuditTrail events={read.data!.audit} />
          </section>
        </>
      )}
    </Drawer>
  );
}
