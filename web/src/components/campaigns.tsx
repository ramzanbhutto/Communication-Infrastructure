import { useEffect, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import {
  ArrowRight,
  CalendarClock,
  Check,
  Mail,
  MessageSquare,
  Plus,
  ShieldCheck,
  Trash2
} from 'lucide-react';
import { ApiError, post, message } from '../api/client';
import { useRead } from '../api/use-read';
import type {
  Campaign,
  CampaignDetail,
  CampaignInput,
  CampaignList,
  CampaignStep
} from '../api/campaigns';
import type { ContactSummary } from '../api/types';
import type { JobDetail } from '../api/infrastructure';
import {
  AuditTrail,
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

const initial: CampaignInput = {
  name: 'Requested property updates',
  timezone: 'UTC',
  startHour: 0,
  endHour: 24,
  weekdaysOnly: false,
  assetIds: ['email-north'],
  steps: [
    {
      channel: 'email',
      subject: 'Your requested property details',
      body: 'Hello {name}. Your synthetic property details are ready for review.',
      delaySeconds: 0
    },
    {
      channel: 'email',
      subject: 'Any questions about the property?',
      body: 'Hello {name}. Would you like any additional synthetic property details?',
      delaySeconds: 600
    }
  ]
};
const reasons: Record<string, string> = {
  ENROLLED: 'Ready for scheduling',
  PREVIOUS_STEP_CONFIRMED: 'Previous delivery confirmed',
  JOB_QUEUED: 'Waiting for submission',
  WAITING_FOR_RECEIPT: 'Waiting for confirmed delivery',
  INBOUND_REPLY: 'Reply received. Automation stopped',
  EMAIL_DIAGNOSTICS_REQUIRED: 'Email configuration needs review',
  OUTSIDE_SENDING_WINDOW: 'Outside the recipient sending window',
  AMBIGUOUS_DELIVERY: 'Submission outcome is uncertain. No resend',
  TOUCH_LIMIT: 'Contact limit reached',
  ASSET_DAILY_CAP: 'Sending asset daily capacity reached',
  SUPPRESSED: 'Contact suppressed',
  OPTED_OUT: 'Recipient opted out',
  CAMPAIGN_CANCELLED: 'Campaign cancelled'
};
export function Campaigns({
  revision,
  operator,
  changed,
  fresh,
  busy,
  asset,
  decision,
  email
}: {
  revision: number;
  operator: boolean;
  changed: () => void;
  fresh: (d: Date) => void;
  busy: (b: boolean) => void;
  asset: (id: string) => void;
  decision: (id: string) => void;
  email: () => void;
}) {
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<string | null>(null);
  const [enrollmentPage, setEnrollmentPage] = useState(1);
  const [editor, setEditor] = useState(false);
  const [editing, setEditing] = useState<Campaign | null>(null);
  const [form, setForm] = useState<CampaignInput>(initial);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [contacts, setContacts] = useState<string[]>(['c102']);
  const [zone, setZone] = useState('UTC');
  const [jobId, setJobId] = useState<string | null>(null);
  const [cancelOpen, setCancelOpen] = useState(false);
  const list = useRead<CampaignList>(
    `/campaigns?search=${encodeURIComponent(search)}&page=${page}`,
    revision,
    3000
  );
  const detail = useRead<CampaignDetail>(
    selected ? `/campaigns/${encodeURIComponent(selected)}?page=${enrollmentPage}` : null,
    revision,
    2000
  );
  const people = useRead<{ items: ContactSummary[] }>('/contacts', revision);
  const job = useRead<JobDetail>(
    jobId ? `/infrastructure/jobs/${encodeURIComponent(jobId)}` : null,
    revision,
    2000
  );
  useEffect(() => {
    if (list.lastSuccess) fresh(list.lastSuccess);
  }, [list.lastSuccess, fresh]);

  const mutate = async (action: () => Promise<void>) => {
    setPending(true);
    busy(true);
    setError(null);
    setNotice(null);
    try {
      await action();
      changed();
    } catch (e) {
      setError(message(e));
    } finally {
      setPending(false);
      busy(false);
    }
  };
  const save = () =>
    void mutate(async () => {
      const result = await post<{ campaign: Campaign }>(
        editing ? `/campaigns/${editing.id}/draft` : '/campaigns',
        { ...form, expectedVersion: editing?.version ?? 0 }
      );
      setSearch('');
      setPage(1);
      setSelected(result.campaign.id);
      setEditor(false);
      setNotice('Draft saved. No outreach has been queued.');
    });
  const act = (action: string) => {
    if (!detail.data) return;
    const c = detail.data.campaign;
    void mutate(async () => {
      await post(`/campaigns/${c.id}/${action}`, { expectedVersion: c.version });
      setCancelOpen(false);
      setNotice(
        `Campaign ${action === 'cancel' ? 'cancelled' : action === 'pause' ? 'paused' : 'activated'}. Recorded state confirmed.`
      );
    });
  };
  const enroll = () => {
    if (!selected) return;
    void mutate(async () => {
      const result = await post<{
        results: { contactId: string; state: string; reason: string }[];
      }>(`/campaigns/${selected}/enroll`, { contactIds: contacts, timezone: zone });
      setNotice(
        result.results
          .map(
            (r) =>
              `${r.contactId}: ${r.state.replaceAll('_', ' ')}${r.reason ? ` (${reasons[r.reason] ?? r.reason})` : ''}`
          )
          .join('; ')
      );
    });
  };
  const updateStep = (index: number, patch: Partial<CampaignStep>) =>
    setForm((f) => ({
      ...f,
      steps: f.steps.map((s, i) => (i === index ? { ...s, ...patch } : s))
    }));
  useEffect(() => {
    if (detail.error instanceof ApiError && detail.error.status === 404) {
      setSelected(null);
      setJobId(null);
    }
  }, [detail.error]);
  const c = detail.data?.campaign;
  return (
    <div className="stack">
      <div className="campaign-intro">
        <div>
          <Badge tone="demo">
            <ShieldCheck size={12} /> SYNTHETIC CAMPAIGNS
          </Badge>
          <h2>Every step has a reason.</h2>
          <p>Scheduled work, verified outcomes and a clear handoff when someone replies.</p>
        </div>
        <button
          className="button primary"
          disabled={!operator || pending}
          onClick={() => {
            setEditing(null);
            setForm(structuredClone(initial));
            setError(null);
            setEditor(true);
          }}
        >
          <Plus size={16} />
          Create campaign
        </button>
      </div>
      {!operator && (
        <p className="meta">
          Reviewer access: inspect sequences, delivery evidence and audit history. Operators manage
          campaigns.
        </p>
      )}
      {error && <Notice error>{error}</Notice>}
      {notice && <Notice onClose={() => setNotice(null)}>{notice}</Notice>}
      {list.error && (
        <Notice error>
          {list.error.message}
          {list.data ? ' Previously loaded data remains visible.' : ''}
        </Notice>
      )}
      <div className="campaign-workspace">
        <div className="stack">
          <SearchField
            label="Search campaigns"
            placeholder="Find a campaign"
            value={search}
            onChange={(v) => {
              setSearch(v);
              setPage(1);
            }}
          />
          {!list.data ? (
            <Loading />
          ) : !list.data.items.length ? (
            <Panel>
              <Empty title="No campaigns in this view">
                Create a draft to define your first sequence. Synthetic recipients only.
              </Empty>
            </Panel>
          ) : (
            <div className="campaign-list">
              {list.data.items.map((item) => {
                const states = list.data!.summaries[item.id] ?? {};
                const n = Object.values(states).reduce((a, b) => a + b, 0);
                return (
                  <button
                    key={item.id}
                    className={`campaign-card ${selected === item.id ? 'selected' : ''}`}
                    aria-pressed={selected === item.id}
                    onClick={() => {
                      setSelected(item.id);
                      setZone(item.timezone);
                      setEnrollmentPage(1);
                      setError(null);
                      setNotice(null);
                    }}
                  >
                    <div className="row between">
                      <Mail size={18} />
                      <Status value={item.state} />
                    </div>
                    <h3>{item.name}</h3>
                    <p>
                      {item.steps.length} steps · {n} enrolled
                    </p>
                    <div className="campaign-card-foot">
                      <span>
                        {states.replied ?? 0} replied · {states.completed ?? 0} completed
                      </span>
                      <ArrowRight size={14} />
                    </div>
                  </button>
                );
              })}
            </div>
          )}
          {list.data && list.data.total > 20 && (
            <div className="action-buttons">
              <button
                className="button"
                disabled={page === 1}
                onClick={() => setPage((p) => p - 1)}
              >
                Previous
              </button>
              <span className="meta">Page {page}</span>
              <button
                className="button"
                disabled={page * 20 >= list.data.total}
                onClick={() => setPage((p) => p + 1)}
              >
                Next
              </button>
            </div>
          )}
        </div>
        {!selected ? (
          <Panel>
            <Empty title="Open a campaign">
              Inspect its sequence, recipients and evidence. Start with an email and a delayed
              follow-up.
            </Empty>
          </Panel>
        ) : (
          <div className="stack">
            {detail.error && <Notice error>{detail.error.message}</Notice>}
            {!c ? (
              <Loading />
            ) : (
              <>
                <Panel
                  title={c.name}
                  subtitle={`Revision ${c.version} · ${c.timezone} default · ${c.startHour}:00 to ${c.endHour}:00${c.weekdaysOnly ? ' · weekdays' : ''}`}
                  action={<Status value={c.state} />}
                >
                  <div className="panel-body stack">
                    <div className="action-buttons">
                      {c.state === 'draft' && (
                        <>
                          <button
                            className="button primary"
                            disabled={!operator || pending}
                            onClick={() => act('activate')}
                          >
                            <Check size={14} />
                            Activate
                          </button>
                          <button
                            className="button"
                            disabled={!operator || pending}
                            onClick={() => {
                              setEditing(c);
                              setForm({ ...c });
                              setEditor(true);
                            }}
                          >
                            Edit draft
                          </button>
                        </>
                      )}
                      {c.state === 'active' && (
                        <button
                          className="button"
                          disabled={!operator || pending}
                          onClick={() => act('pause')}
                        >
                          Pause campaign
                        </button>
                      )}
                      {c.state === 'paused' && (
                        <button
                          className="button primary"
                          disabled={!operator || pending}
                          onClick={() => act('resume')}
                        >
                          Resume campaign
                        </button>
                      )}
                      {c.state !== 'cancelled' && (
                        <button
                          className="button danger"
                          disabled={!operator || pending}
                          onClick={() => setCancelOpen(true)}
                        >
                          Cancel campaign
                        </button>
                      )}
                      <button className="button" onClick={email}>
                        Review email configuration
                      </button>
                    </div>
                    <div className="sequence-track">
                      {c.steps.map((step, i) => (
                        <article className="sequence-node" key={i}>
                          <div className="sequence-index">{i + 1}</div>
                          <div>
                            <div className="row">
                              {step.channel === 'email' ? (
                                <Mail size={15} />
                              ) : (
                                <MessageSquare size={15} />
                              )}
                              <strong>
                                {step.channel === 'email' ? 'Email' : 'Consented SMS'}
                              </strong>
                              <Badge>
                                {step.delaySeconds === 0
                                  ? 'When eligible'
                                  : `After ${step.delaySeconds / 60} min`}
                              </Badge>
                            </div>
                            <h3>{step.subject || 'SMS message'}</h3>
                            <p>{step.body}</p>
                          </div>
                        </article>
                      ))}
                    </div>
                    <p className="meta">
                      Delays follow confirmed delivery. Replies stop automation across channels.
                      Activated steps are immutable.
                    </p>
                    <div className="action-buttons">
                      {c.assetIds.map((id) => (
                        <TextLink key={id} onClick={() => asset(id)}>
                          Inspect {id}
                        </TextLink>
                      ))}
                    </div>
                  </div>
                </Panel>
                <Panel
                  title="Recipient progress"
                  subtitle="Masked contact identifiers. Times use your browser time zone; scheduling uses the recorded recipient zone."
                >
                  {detail.data!.enrollments.length ? (
                    <div className="table-scroll">
                      <table className="table campaign-table">
                        <thead>
                          <tr>
                            <th>Contact / step</th>
                            <th>Recorded outcome</th>
                            <th>Next eligible time</th>
                            <th>Evidence</th>
                          </tr>
                        </thead>
                        <tbody>
                          {detail.data!.enrollments.map((e) => {
                            const x =
                              detail.data!.executions.find(
                                (x) => x.enrollmentId === e.id && x.step === e.step
                              ) ??
                              detail.data!.executions.filter((x) => x.enrollmentId === e.id).at(-1);
                            const terminal = ['replied', 'completed', 'stopped'].includes(e.state);
                            return (
                              <tr key={e.id}>
                                <td>
                                  <strong>{e.contactId}</strong>
                                  <div className="cell-meta">
                                    {Math.min(e.step + 1, c.steps.length)} / {c.steps.length} ·{' '}
                                    {e.timezone}
                                  </div>
                                </td>
                                <td>
                                  <Status value={e.state} />
                                  <p className="cell-meta long">
                                    {reasons[e.reason] ??
                                      e.reason.replaceAll('_', ' ').toLowerCase()}
                                  </p>
                                </td>
                                <td>
                                  {terminal ? (
                                    <span className="meta">No further work</span>
                                  ) : (
                                    <Stamp value={e.nextRunAt} />
                                  )}
                                </td>
                                <td>
                                  {x ? (
                                    <div>
                                      <button
                                        className="link-button"
                                        onClick={() => setJobId(x.jobId)}
                                      >
                                        Inspect delivery
                                      </button>
                                      {x.decisionId && (
                                        <button
                                          className="link-button"
                                          onClick={() => decision(x.decisionId!)}
                                        >
                                          Policy decision
                                        </button>
                                      )}
                                    </div>
                                  ) : (
                                    <span className="meta">No job submitted</span>
                                  )}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  ) : (
                    <Empty title="No recipients enrolled">
                      Activate this campaign and enroll a synthetic contact to start the sequence.
                    </Empty>
                  )}
                  {detail.data!.total > 50 && (
                    <div className="pagination">
                      <button
                        className="button"
                        disabled={enrollmentPage === 1}
                        onClick={() => setEnrollmentPage((p) => p - 1)}
                      >
                        Previous
                      </button>
                      <span>
                        Page {enrollmentPage} · {detail.data!.total} enrollments
                      </span>
                      <button
                        className="button"
                        disabled={enrollmentPage * 50 >= detail.data!.total}
                        onClick={() => setEnrollmentPage((p) => p + 1)}
                      >
                        Next
                      </button>
                    </div>
                  )}
                </Panel>
                {c.state === 'active' && (
                  <Panel
                    title="Enroll synthetic recipients"
                    subtitle="Select contacts and explicitly record their time zone. Consent is checked on the server."
                  >
                    <div className="panel-body stack">
                      <div className="contact-options">
                        {people.data?.items.map((p) => (
                          <label key={p.id}>
                            <input
                              type="checkbox"
                              checked={contacts.includes(p.id)}
                              disabled={!operator || pending}
                              onChange={(e) =>
                                setContacts((ids) =>
                                  e.target.checked
                                    ? [...ids, p.id]
                                    : ids.filter((id) => id !== p.id)
                                )
                              }
                            />
                            <span>
                              {p.id}
                              <small>{p.email}</small>
                            </span>
                          </label>
                        ))}
                      </div>
                      {people.error && <Notice error>{people.error.message}</Notice>}
                      <label className="field">
                        Recipient time zone
                        <input
                          type="text"
                          value={zone}
                          disabled={!operator || pending}
                          onChange={(e) => setZone(e.target.value)}
                          maxLength={80}
                        />
                      </label>
                      <button
                        className="button primary"
                        disabled={!operator || pending || !contacts.length || !people.data}
                        onClick={enroll}
                      >
                        Enroll selected contacts
                      </button>
                    </div>
                  </Panel>
                )}
                <Panel
                  title="Campaign audit"
                  subtitle="State changes and enrollment outcomes are recorded in PostgreSQL."
                >
                  <div className="panel-body">
                    <AuditTrail events={detail.data!.audit} onAsset={asset} onDecision={decision} />
                  </div>
                </Panel>
              </>
            )}
          </div>
        )}
      </div>
      <Dialog.Root
        open={editor}
        onOpenChange={(open) => {
          if (!pending) setEditor(open);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="overlay" />
          <Dialog.Content className="modal campaign-editor">
            <Dialog.Title>{editing ? 'Edit campaign draft' : 'Create campaign'}</Dialog.Title>
            <Dialog.Description>
              Define a short synthetic sequence. Activation freezes its steps.
            </Dialog.Description>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                save();
              }}
              className="stack"
            >
              {error && <Notice error>{error}</Notice>}
              <label className="field">
                Campaign name
                <input
                  type="text"
                  required
                  minLength={3}
                  maxLength={100}
                  value={form.name}
                  disabled={pending}
                  onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                />
              </label>
              <fieldset className="campaign-assets">
                <legend>Sending assets</legend>
                {list.data?.assets
                  .filter((a) => a.kind !== 'imessage')
                  .map((a) => (
                    <label key={a.id}>
                      <input
                        type="checkbox"
                        checked={form.assetIds.includes(a.id)}
                        disabled={pending}
                        onChange={(e) =>
                          setForm((f) => ({
                            ...f,
                            assetIds: e.target.checked
                              ? [...f.assetIds, a.id]
                              : f.assetIds.filter((id) => id !== a.id)
                          }))
                        }
                      />
                      {a.name}
                    </label>
                  ))}
              </fieldset>
              <div className="form-columns">
                <label className="field">
                  Default time zone
                  <input
                    type="text"
                    required
                    value={form.timezone}
                    disabled={pending}
                    onChange={(e) => setForm((f) => ({ ...f, timezone: e.target.value }))}
                  />
                </label>
                <label className="field">
                  Start hour
                  <input
                    className="input"
                    type="number"
                    min={0}
                    max={23}
                    required
                    value={form.startHour}
                    disabled={pending}
                    onChange={(e) => setForm((f) => ({ ...f, startHour: Number(e.target.value) }))}
                  />
                </label>
                <label className="field">
                  End hour
                  <input
                    className="input"
                    type="number"
                    min={1}
                    max={24}
                    required
                    value={form.endHour}
                    disabled={pending}
                    onChange={(e) => setForm((f) => ({ ...f, endHour: Number(e.target.value) }))}
                  />
                </label>
              </div>
              <label className="checkbox-line">
                <input
                  type="checkbox"
                  checked={form.weekdaysOnly}
                  disabled={pending}
                  onChange={(e) => setForm((f) => ({ ...f, weekdaysOnly: e.target.checked }))}
                />
                Weekdays only
              </label>
              <p className="meta">
                The default demo window is 24 hours. Choose recipient-appropriate hours for other
                scenarios.
              </p>
              {form.steps.map((step, i) => (
                <fieldset className="step-editor" key={i}>
                  <legend>Step {i + 1}</legend>
                  <div className="row between">
                    <label className="field">
                      Channel
                      <select
                        value={step.channel}
                        disabled={pending}
                        onChange={(e) =>
                          updateStep(i, { channel: e.target.value as 'email' | 'sms' })
                        }
                      >
                        <option value="email">Email</option>
                        <option value="sms">Consented SMS</option>
                      </select>
                    </label>
                    <label className="field">
                      Delay in minutes
                      <input
                        className="input"
                        type="number"
                        min={0}
                        max={43200}
                        required
                        value={step.delaySeconds / 60}
                        disabled={pending}
                        onChange={(e) =>
                          updateStep(i, { delaySeconds: Number(e.target.value) * 60 })
                        }
                      />
                    </label>
                    <button
                      type="button"
                      className="button icon-button"
                      aria-label={`Remove step ${i + 1}`}
                      disabled={pending || form.steps.length === 1}
                      onClick={() =>
                        setForm((f) => ({ ...f, steps: f.steps.filter((_, n) => n !== i) }))
                      }
                    >
                      <Trash2 size={15} />
                    </button>
                  </div>
                  {step.channel === 'email' && (
                    <label className="field">
                      Subject
                      <input
                        type="text"
                        required
                        maxLength={200}
                        value={step.subject}
                        disabled={pending}
                        onChange={(e) => updateStep(i, { subject: e.target.value })}
                      />
                    </label>
                  )}
                  <label className="field">
                    Message
                    <textarea
                      required
                      maxLength={480}
                      value={step.body}
                      disabled={pending}
                      onChange={(e) => updateStep(i, { body: e.target.value })}
                    />
                  </label>
                  <small>
                    Supported fields: {'{name}'} and {'{contact_id}'}. Plain text only.
                  </small>
                </fieldset>
              ))}
              <button
                type="button"
                className="button"
                disabled={pending || form.steps.length >= 6}
                onClick={() =>
                  setForm((f) => ({
                    ...f,
                    steps: [
                      ...f.steps,
                      {
                        channel: 'email',
                        subject: 'Follow-up',
                        body: 'Hello {name}. Your requested update is ready.',
                        delaySeconds: 86400
                      }
                    ]
                  }))
                }
              >
                <Plus size={15} />
                Add step
              </button>
              <div className="action-buttons">
                <Dialog.Close className="button" disabled={pending}>
                  Close
                </Dialog.Close>
                <button className="button primary" disabled={pending || !form.assetIds.length}>
                  {pending ? 'Saving…' : 'Save draft'}
                </button>
              </div>
            </form>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
      <Dialog.Root
        open={cancelOpen}
        onOpenChange={(open) => {
          if (!pending) setCancelOpen(open);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="overlay" />
          <Dialog.Content className="modal">
            <Dialog.Title>Cancel this campaign?</Dialog.Title>
            <Dialog.Description>
              Unsent campaign jobs will be suppressed. Already submitted jobs retain their evidence
              and cannot be recalled. Cancellation is permanent.
            </Dialog.Description>
            {error && <Notice error>{error}</Notice>}
            <div className="action-buttons">
              <Dialog.Close className="button" disabled={pending}>
                Keep campaign
              </Dialog.Close>
              <button className="button danger" disabled={pending} onClick={() => act('cancel')}>
                Confirm cancellation
              </button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
      <Drawer
        open={jobId !== null}
        onClose={() => setJobId(null)}
        title="Campaign delivery evidence"
        subtitle="Persisted state and authenticated local provider events. This action never resends."
        eyebrow="SYNTHETIC DELIVERY"
      >
        {job.error && <Notice error>{job.error.message}</Notice>}
        {!job.data ? (
          <Loading />
        ) : (
          <>
            <Status value={job.data.job.state} />
            <dl className="fact-list">
              <div>
                <dt>Submission attempts</dt>
                <dd>{job.data.job.attempts}</dd>
              </div>
              <div>
                <dt>Provider record</dt>
                <dd className="mono">{job.data.job.providerId ?? 'Not available'}</dd>
              </div>
            </dl>
            {job.data.job.lastError && <Notice warning>{job.data.job.lastError}</Notice>}
            {job.data.job.decisionId && (
              <TextLink
                onClick={() => {
                  setJobId(null);
                  decision(job.data!.job.decisionId!);
                }}
              >
                Inspect policy decision
              </TextLink>
            )}
            {job.data.events.map((e) => (
              <article className="detail-block" key={e.id}>
                <strong>{e.kind}</strong>
                <p className="meta">
                  {e.source} · <Stamp value={e.occurredAt} />
                </p>
              </article>
            ))}
            {!job.data.events.length && (
              <p className="meta">No verified receipt is recorded yet.</p>
            )}
          </>
        )}
      </Drawer>
      <div className="data-note">
        <CalendarClock size={13} />
        PostgreSQL scheduling. Existing consent rules apply. No real outreach.
      </div>
    </div>
  );
}
