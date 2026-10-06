import { useEffect, useState, type FormEvent } from 'react';
import { ArrowUpRight, MessageSquare, Phone, Send } from 'lucide-react';
import { ApiError, message } from '../api/client';
import { outreach, setReplyConsumer } from '../api/conversations';
import { useRead } from '../api/use-read';
import type {
  ContactDetail,
  ContactSummary,
  Conversations as ConversationData,
  OutreachResult
} from '../api/types';
import { Badge, Empty, Loading, Notice, Panel, Stamp, Status, TextLink } from './ui';

const explanations: Record<string, string> = {
  ELIGIBLE:
    'The current consent and suppression records pass the preview checks. The server will validate them again.',
  NO_SMS_CONSENT:
    'Explicit SMS consent is missing. An email open is not SMS permission. An attempt will be blocked and recorded.',
  NO_WARM_REPLY:
    'A recorded inbound reply is required for warm SMS. Email opens do not meet this requirement.',
  DNC_SUPPRESSED: 'This contact is on the synthetic DNC list. Calls and SMS are blocked.',
  OPTED_OUT: 'This contact opted out. Phone outreach is suppressed.',
  LINE_QUARANTINED: 'This line is quarantined. Choose an active source line.'
};

export function Conversations({
  revision,
  operator,
  initialContact,
  changed,
  decision,
  asset,
  fresh,
  busy
}: {
  revision: number;
  operator: boolean;
  initialContact: string | null;
  changed: () => void;
  decision: (id: string) => void;
  asset: (id: string) => void;
  fresh: (date: Date) => void;
  busy: (value: boolean) => void;
}) {
  const [channel, setChannel] = useState<'call' | 'sms'>('sms');
  const [contactId, setContactId] = useState(initialContact ?? 'c101');
  const [lineId, setLineId] = useState('line-cedar');
  const [body, setBody] = useState(
    'Thanks for your interest. Would you like the property details?'
  );
  const [pending, setPending] = useState(false);
  const [queuePending, setQueuePending] = useState(false);
  const [failure, setFailure] = useState<ApiError | Error | null>(null);
  const [result, setResult] = useState<OutreachResult | null>(null);
  const [queueNotice, setQueueNotice] = useState<string | null>(null);
  const [queueError, setQueueError] = useState(false);
  const read = useRead<ConversationData>('/conversations', revision, 2000);
  const contacts = useRead<{ items: ContactSummary[] }>('/contacts', revision);
  const details = useRead<ContactDetail>(
    `/contacts/${encodeURIComponent(contactId)}`,
    revision,
    5000
  );
  useEffect(() => {
    if (read.lastSuccess) fresh(read.lastSuccess);
  }, [read.lastSuccess, fresh]);
  useEffect(() => {
    if (initialContact) setContactId(initialContact);
  }, [initialContact]);
  useEffect(() => {
    setFailure(null);
    setResult(null);
  }, [channel, contactId, lineId, body]);
  const activeCall = read.data?.calls.find((call) => call.status === 'active');
  const code = details.data?.eligibility[lineId]?.[channel];
  const line = read.data?.assets.find((item) => item.id === lineId);
  const bodyLength = Array.from(body.trim()).length;
  const disabled =
    !operator ||
    pending ||
    queuePending ||
    Boolean(read.error) ||
    Boolean(details.error) ||
    !details.data ||
    !read.data ||
    !line ||
    line.status !== 'active' ||
    (channel === 'call' && Boolean(activeCall));
  const send = async (event: FormEvent) => {
    event.preventDefault();
    if (disabled) return;
    setPending(true);
    busy(true);
    setFailure(null);
    setResult(null);
    // Each deliberate attempt gets a key. The browser never retries a failed mutation.
    try {
      const response = await outreach(
        channel,
        contactId,
        lineId,
        channel === 'sms' ? body.trim() : '',
        crypto.randomUUID()
      );
      setResult(response);
      changed();
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(message(error)));
      changed();
    } finally {
      setPending(false);
      busy(false);
    }
  };
  const consumer = async () => {
    if (!read.data || queuePending) return;
    setQueuePending(true);
    busy(true);
    setQueueNotice(null);
    try {
      const result = await setReplyConsumer(!read.data.queue.paused);
      setQueueError(false);
      setQueueNotice(
        result.paused
          ? 'Consumer pause recorded. Queued events remain durable.'
          : 'Consumer resumed. Watch for persisted replies and a confirmed backlog change.'
      );
      changed();
    } catch (error) {
      setQueueError(true);
      setQueueNotice(message(error));
    } finally {
      setQueuePending(false);
      busy(false);
    }
  };
  return (
    <>
      {read.error && (
        <Notice error>
          {read.error.message}
          {read.data && ' Existing replies remain visible from the last successful snapshot.'}
        </Notice>
      )}
      {queueNotice && (
        <Notice error={queueError} onClose={() => setQueueNotice(null)}>
          {queueNotice}
        </Notice>
      )}
      {!read.data ? (
        !read.error && <Loading />
      ) : (
        <div className="conversations-grid">
          <div className="stack">
            <Panel
              title="Reply queue"
              subtitle="Stored replies and actual transport state"
              action={<Badge>{read.data.replies.length} recent replies</Badge>}
            >
              <div className="queue-banner">
                <div>
                  <h3>
                    {read.data.queue.paused
                      ? 'Consumer paused'
                      : read.data.queue.state === 'unavailable'
                        ? 'Transport unavailable'
                        : 'Consumer enabled'}{' '}
                    ·{' '}
                    {read.data.queue.pending === null
                      ? 'Backlog unavailable'
                      : `${read.data.queue.pending} transport entries`}
                  </h3>
                  <p>
                    {read.data.queue.unpublished} unpublished outbox events · Checked{' '}
                    <Stamp value={read.data.queue.checkedAt} />
                  </p>
                </div>
                <button
                  className="button"
                  disabled={!operator || queuePending || pending}
                  onClick={() => void consumer()}
                >
                  {queuePending
                    ? 'Confirming…'
                    : read.data.queue.paused
                      ? 'Resume consumer'
                      : 'Pause consumer'}
                </button>
              </div>
              {read.data.queue.error && (
                <div className="panel-body">
                  <Notice warning>
                    {read.data.queue.error} The PostgreSQL reply list below is still real stored
                    data.
                  </Notice>
                </div>
              )}
              {!read.data.replies.length ? (
                <Empty title="No persisted replies">
                  Queued events appear here only after the worker commits them to PostgreSQL.
                </Empty>
              ) : (
                read.data.replies.map((reply) => (
                  <article className="reply" key={reply.id}>
                    <div className="reply-context">
                      <strong className="cell-title">{reply.contactLabel}</strong>
                      <Badge
                        tone={
                          reply.tag === 'opt-out'
                            ? 'blocked'
                            : reply.tag === 'interested'
                              ? 'good'
                              : ''
                        }
                      >
                        {reply.tag}
                      </Badge>
                    </div>
                    <p>{reply.body}</p>
                    <div className="meta section-gap">
                      Received <Stamp value={reply.receivedAt} /> · Persisted{' '}
                      <Stamp value={reply.persistedAt} /> · Simulated inbound event
                    </div>
                    <div className="row">
                      <TextLink
                        onClick={() => {
                          setContactId(reply.contactId);
                          setChannel('sms');
                          document.getElementById('outreach-composer')?.scrollIntoView({
                            behavior: matchMedia('(prefers-reduced-motion: reduce)').matches
                              ? 'auto'
                              : 'smooth',
                            block: 'start'
                          });
                        }}
                      >
                        Contact workflow
                      </TextLink>
                      <TextLink onClick={() => asset(reply.assetId)}>Sending asset</TextLink>
                    </div>
                  </article>
                ))
              )}
            </Panel>
            <Panel
              title="Recent calling sessions"
              subtitle="Server-enforced single active call per workspace"
            >
              {!read.data.calls.length ? (
                <Empty title="No calling sessions yet">
                  Choose a permitted contact and active line to start a simulated six-second call.
                </Empty>
              ) : (
                <div className="panel-body stack">
                  {read.data.calls.map((call) => (
                    <div className="call-session" key={call.id}>
                      <div className="row between">
                        <strong>Contact · {call.contactId}</strong>
                        <Status value={call.status} />
                      </div>
                      <p className="meta">
                        {call.outcome?.replaceAll('_', ' ') ??
                          'Waiting for the simulated provider outcome.'}
                        <br />
                        Started <Stamp value={call.startedAt} />
                        {call.completedAt && (
                          <>
                            {' '}
                            · Completed <Stamp value={call.completedAt} />
                          </>
                        )}
                      </p>
                      <div className="row">
                        <TextLink onClick={() => decision(call.decisionId)}>
                          Inspect decision
                        </TextLink>
                        <TextLink onClick={() => asset(call.assetId)}>Source line</TextLink>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </Panel>
          </div>
          <aside className="conversations-compose" id="outreach-composer">
            <Panel
              title="Human-assisted outreach"
              subtitle="Local simulation. No provider credentials or real delivery."
            >
              <form className="compose panel-body" onSubmit={(event) => void send(event)}>
                <div className="compose-tabs">
                  {(['sms', 'call'] as const).map((value) => (
                    <button
                      key={value}
                      type="button"
                      className={`button ${channel === value ? 'primary' : ''}`}
                      aria-pressed={channel === value}
                      disabled={pending}
                      onClick={() => setChannel(value)}
                    >
                      {value === 'sms' ? (
                        <>
                          <MessageSquare size={15} />
                          Warm SMS
                        </>
                      ) : (
                        <>
                          <Phone size={15} />
                          Assisted call
                        </>
                      )}
                    </button>
                  ))}
                </div>
                {contacts.error && <Notice error>{contacts.error.message}</Notice>}
                <div className="field">
                  <label htmlFor="outreach-contact">Contact</label>
                  <select
                    id="outreach-contact"
                    value={contactId}
                    onChange={(event) => setContactId(event.target.value)}
                    disabled={pending || !contacts.data}
                  >
                    {contacts.data?.items.map((item) => (
                      <option key={item.id} value={item.id}>
                        {item.label} · {item.phone}
                      </option>
                    ))}
                  </select>
                </div>
                <div className="field">
                  <label htmlFor="outreach-line">Source line</label>
                  <select
                    id="outreach-line"
                    value={lineId}
                    onChange={(event) => setLineId(event.target.value)}
                    disabled={pending}
                  >
                    {read.data.assets
                      .filter((item) => item.kind === 'phone')
                      .map((item) => (
                        <option key={item.id} value={item.id} disabled={item.status !== 'active'}>
                          {item.name} · {item.status}
                          {item.status === 'quarantined' ? ' (unavailable)' : ''}
                        </option>
                      ))}
                  </select>
                </div>
                {details.error && <Notice error>{details.error.message}</Notice>}
                {details.data && (
                  <div className="contact-detail">
                    <strong>Authorized contact detail</strong>
                    <br />
                    {details.data.contact.name}
                    <br />
                    {details.data.contact.phone}
                    <br />
                    {details.data.contact.email}
                    <br />
                    <span className="muted">
                      Synthetic contact. Overview screens remain masked.
                    </span>
                  </div>
                )}
                {code && (
                  <div className={`eligibility ${code !== 'ELIGIBLE' ? 'blocked' : ''}`}>
                    <strong>
                      {code === 'ELIGIBLE'
                        ? 'Current eligibility preview'
                        : 'Eligibility check will block outreach'}
                    </strong>
                    <br />
                    {explanations[code] ?? code}
                    <br />
                    <span className="mono">{code}</span>
                  </div>
                )}
                {channel === 'sms' && (
                  <div className="field">
                    <label htmlFor="outreach-message">Message</label>
                    <textarea
                      id="outreach-message"
                      aria-describedby="message-guidance"
                      required
                      maxLength={960}
                      value={body}
                      onChange={(event) => setBody(event.target.value)}
                      disabled={pending}
                    />
                    <span className="meta" id="message-guidance">
                      {bodyLength} / 480 characters. Explicit SMS consent and an inbound reply are
                      required.
                    </span>
                  </div>
                )}
                {activeCall && (
                  <div className="eligibility">
                    <strong>Call in progress</strong>
                    <br />
                    Contact · {activeCall.contactId}. New calls are unavailable until the server
                    records completion.
                  </div>
                )}
                {failure && (
                  <Notice error>
                    {failure.message}
                    {failure instanceof ApiError && failure.decisionId && (
                      <>
                        <br />
                        <TextLink onClick={() => decision(failure.decisionId!)}>
                          Inspect the recorded rejection
                        </TextLink>
                      </>
                    )}
                  </Notice>
                )}
                {result && (
                  <Notice>
                    {result.status === 'active'
                      ? 'The simulated call is active. Its final outcome will appear in recent sessions.'
                      : 'The simulated message receipt is stored. No real SMS was sent.'}
                    <br />
                    <TextLink onClick={() => decision(result.decisionId)}>
                      Inspect the recorded decision
                    </TextLink>
                  </Notice>
                )}
                {!operator && (
                  <Notice warning>
                    The reviewer has read-only access. Switch to the demo operator for simulated
                    actions.
                  </Notice>
                )}
                <div className="compose-action">
                  <button
                    className="button primary"
                    disabled={
                      disabled || (channel === 'sms' && (bodyLength < 1 || bodyLength > 480))
                    }
                  >
                    {pending ? (
                      'Waiting for confirmation…'
                    ) : channel === 'sms' ? (
                      <>
                        <Send size={15} />
                        Check and simulate SMS
                      </>
                    ) : (
                      <>
                        <Phone size={15} />
                        Start simulated call
                      </>
                    )}
                  </button>
                  <p className="meta">
                    An attempt records the server decision. Blocked contacts receive no simulated
                    delivery. Failed actions are never retried automatically.
                  </p>
                </div>
              </form>
            </Panel>
            <p className="data-note">
              <ArrowUpRight size={12} />
              Reply opt-outs suppress future phone outreach.
            </p>
          </aside>
        </div>
      )}
    </>
  );
}
