import { useEffect, useState, type FormEvent } from 'react';
import { ArrowRight, Cable, CheckCheck, Clock3, Mail, Send, ShieldCheck } from 'lucide-react';
import { ApiError, message, post } from '../api/client';
import type {
  DeliveryChannel,
  InfrastructureData,
  InboxDetail,
  JobDetail,
  Scenario
} from '../api/infrastructure';
import { useRead } from '../api/use-read';
import { Badge, Drawer, Empty, Loading, Notice, Panel, SearchField, Stamp, Status } from './ui';
import { IMessageOption } from './imessage-option';

const scenarioText: Record<Scenario, string> = {
  success: 'The lab accepts the request and sends a signed completion event.',
  throttle: 'The first request is rejected with HTTP 429. The worker retries with bounded backoff.',
  ambiguous: 'The lab accepts the request but returns HTTP 503. The worker will not resubmit it.',
  filtered: 'The lab reports an undelivered SMS with filtering code 30007.',
  bounce: 'The lab reports an email bounce and the server suppresses future outreach.'
};
export function Infrastructure({
  revision,
  operator,
  changed,
  fresh,
  busy,
  asset,
  decision
}: {
  revision: number;
  operator: boolean;
  changed: () => void;
  fresh: (date: Date) => void;
  busy: (value: boolean) => void;
  asset: (id: string) => void;
  decision: (id: string) => void;
}) {
  const read = useRead<InfrastructureData>('/infrastructure', revision, 1800);
  const [tab, setTab] = useState<'delivery' | 'assets' | 'inbox'>('delivery');
  const [imessageReady, setIMessageReady] = useState(false);
  const [channel, setChannel] = useState<DeliveryChannel>('email');
  const [contactId, setContactId] = useState('c102');
  const [assetId, setAssetId] = useState('email-north');
  const [subject, setSubject] = useState('Requested property details');
  const [body, setBody] = useState('Here are the synthetic property details you requested.');
  const [destination, setDestination] = useState('+12025550188');
  const [scenario, setScenario] = useState<Scenario>('success');
  const [search, setSearch] = useState('');
  const [state, setState] = useState('');
  const [selectedJob, setSelectedJob] = useState<string | null>(null);
  const [selectedReply, setSelectedReply] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [rejectionId, setRejectionId] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [unconfirmed, setUnconfirmed] = useState(false);
  const job = useRead<JobDetail>(
    selectedJob ? `/infrastructure/jobs/${selectedJob}` : null,
    revision,
    1800
  );
  const reply = useRead<InboxDetail>(
    selectedReply ? `/infrastructure/inbox/${selectedReply}` : null,
    revision
  );
  useEffect(() => {
    if (read.lastSuccess) fresh(read.lastSuccess);
  }, [read.lastSuccess, fresh]);
  const act = async (path: string, data: unknown, success: string): Promise<boolean> => {
    if (pending) return false;
    setPending(true);
    busy(true);
    setError(null);
    setRejectionId(null);
    setNotice(null);
    try {
      await post(path, data);
      setNotice(success);
      changed();
      return true;
    } catch (failure) {
      setError(message(failure));
      if (failure instanceof ApiError && failure.decisionId) setRejectionId(failure.decisionId);
      if (failure instanceof ApiError && (failure.status === 0 || failure.status >= 500))
        setUnconfirmed(true);
      return false;
    } finally {
      setPending(false);
      busy(false);
    }
  };
  const chooseChannel = (next: DeliveryChannel) => {
    setChannel(next);
    setIMessageReady(false);
    setScenario('success');
    setAssetId(
      next === 'imessage' ? 'bridge-imessage' : next === 'email' ? 'email-north' : 'line-cedar'
    );
    if (next === 'provision' || next === 'trunk')
      setSubject(next === 'provision' ? 'Lab line 03' : 'Lab secure trunk');
    else setSubject('Requested property details');
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    await act(
      '/infrastructure/jobs',
      {
        channel,
        contactId,
        assetId,
        subject,
        body,
        destination,
        scenario,
        requestKey: crypto.randomUUID()
      },
      'Job queued. Provider acceptance and verified completion appear separately below.'
    );
  };
  if (!read.data)
    return read.error ? (
      <Notice error>{read.error.message}</Notice>
    ) : (
      <Loading text="Loading infrastructure records..." />
    );
  const data = read.data;
  const enabled = data.mode === 'local_lab';
  const management = channel === 'provision' || channel === 'trunk';
  const assets = data.assets.filter(
    (item) =>
      item.kind === (channel === 'imessage' ? 'imessage' : channel === 'email' ? 'email' : 'phone')
  );
  const jobs = data.jobs.filter(
    (item) =>
      (!state || item.state === state) &&
      `${item.id} ${item.channel} ${item.providerId ?? ''} ${item.lastError ?? ''}`
        .toLowerCase()
        .includes(search.toLowerCase())
  );
  const mutationDisabled = !operator || !enabled || pending || unconfirmed;
  return (
    <div className="infra-page">
      <section className="infra-intro">
        <div>
          <Badge tone="demo">
            <Cable size={12} /> LOCAL PROVIDER LAB
          </Badge>
          <h2>From request to evidence.</h2>
          <p>
            Exercise the HTTP adapters, durable jobs and verified event pipeline. Every provider
            outcome here is simulated.
          </p>
        </div>
        <div className="infra-safety">
          <ShieldCheck size={22} />
          <div>
            <strong>Live delivery disabled</strong>
            <span>No provider accounts or real recipients are connected.</span>
          </div>
        </div>
      </section>
      {!enabled && (
        <Notice warning>
          Start the API with INFRA_LAB=true. Existing history remains visible while the lab is
          disabled.
        </Notice>
      )}
      {!operator && (
        <Notice warning>
          Your reviewer role can inspect records. Operational changes require an operator.
        </Notice>
      )}
      {read.error && (
        <Notice error>{read.error.message} Previously loaded records remain visible.</Notice>
      )}
      {error && (
        <Notice error onClose={() => setError(null)}>
          {error}
        </Notice>
      )}
      {rejectionId && (
        <button className="link-button" onClick={() => decision(rejectionId)}>
          Inspect recorded rejection <ArrowRight size={13} />
        </button>
      )}
      {notice && <Notice onClose={() => setNotice(null)}>{notice}</Notice>}
      {unconfirmed && (
        <Notice warning>
          The last request was unconfirmed. Inspect refreshed jobs before another action.{' '}
          <button
            className="link-button"
            onClick={() => {
              setUnconfirmed(false);
              changed();
            }}
          >
            I reviewed the recorded result
          </button>
        </Notice>
      )}
      <div className="metrics infra-metrics">
        <button
          className="metric metric-blue"
          onClick={() => {
            setTab('delivery');
            setState('');
          }}
        >
          <span className="metric-label">
            <Send size={15} /> Queued requests
          </span>
          <strong>{data.metrics.total}</strong>
          <span className="meta">Created in the last 24 hours</span>
        </button>
        <button
          className="metric metric-mint"
          onClick={() => {
            setTab('delivery');
            setState('');
          }}
        >
          <span className="metric-label">
            <CheckCheck size={16} /> Confirmed outcomes
          </span>
          <strong>{data.metrics.confirmed}</strong>
          <span className="meta">Verified lab delivery or resource creation</span>
        </button>
        <button
          className="metric metric-peach"
          onClick={() => {
            setTab('delivery');
            setState('unknown');
          }}
        >
          <span className="metric-label">
            <Clock3 size={16} /> Needs verification
          </span>
          <strong>{data.metrics.unknown}</strong>
          <span className="meta">No automatic resubmission</span>
        </button>
      </div>
      <div className="infra-tabs" aria-label="Infrastructure views">
        {(['delivery', 'assets', 'inbox'] as const).map((key) => (
          <button
            key={key}
            className={tab === key ? 'active' : ''}
            aria-pressed={tab === key}
            disabled={pending}
            onClick={() => setTab(key)}
          >
            {key === 'delivery'
              ? 'Delivery pipeline'
              : key === 'assets'
                ? 'Domains & resources'
                : 'Provider inbox'}
            {key === 'inbox' && (
              <Badge>{data.inbox.filter((item) => item.state === 'open').length}</Badge>
            )}
          </button>
        ))}
        <span className="meta">
          {read.refreshing ? 'Refreshing records...' : 'Updated'} <Stamp value={read.lastSuccess} />
        </span>
      </div>
      {tab === 'delivery' && (
        <div className="infra-grid">
          <Panel
            title="Recorded jobs"
            subtitle="Latest 50 jobs across all dates. Metrics above cover the last 24 hours."
          >
            <div className="filters">
              <SearchField
                label="Search delivery jobs"
                placeholder="Job, provider ID or rejection"
                value={search}
                onChange={setSearch}
              />
              <select
                aria-label="Delivery state"
                value={state}
                onChange={(event) => setState(event.target.value)}
              >
                <option value="">All states</option>
                {[
                  'queued',
                  'submitting',
                  'accepted',
                  'delivered',
                  'completed',
                  'unknown',
                  'failed',
                  'suppressed'
                ].map((value) => (
                  <option key={value}>{value}</option>
                ))}
              </select>
            </div>
            {jobs.length ? (
              <div className="table-scroll">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Request</th>
                      <th>Evidence</th>
                      <th>State</th>
                      <th>Attempts</th>
                      <th>
                        <span className="sr-only">Details</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {jobs.map((item) => (
                      <tr key={item.id}>
                        <td>
                          <strong>
                            {item.channel === 'trunk' ? 'SIP trunk resource' : item.channel}
                          </strong>
                          <div className="meta">
                            {item.contactId ? `Contact · ${item.contactId}` : 'Resource management'}
                          </div>
                          <div className="meta">
                            <Stamp value={item.createdAt} />
                          </div>
                        </td>
                        <td>
                          <span className="mono infra-id">
                            {item.providerId ?? 'No provider ID recorded'}
                          </span>
                          <div className="meta">
                            {item.lastError ?? `Lab scenario: ${item.scenario}`}
                          </div>
                        </td>
                        <td>
                          <Status value={item.state} />
                        </td>
                        <td>{item.attempts}</td>
                        <td>
                          <button
                            className="button icon-button"
                            aria-label={`Inspect job ${item.id}`}
                            onClick={() => setSelectedJob(item.id)}
                          >
                            <ArrowRight size={15} />
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <Empty
                title={search || state ? 'No matching jobs' : 'Your first request starts here'}
              >
                Queue a synthetic email or call to follow the persisted request through the provider
                adapter.
              </Empty>
            )}
          </Panel>
          <Panel
            title="Queue a request"
            subtitle="Synthetic contacts and isolated provider endpoints."
          >
            <form className="compose panel-body" onSubmit={(event) => void submit(event)}>
              <div className="field">
                <label htmlFor="infra-channel">Channel or resource</label>
                <select
                  id="infra-channel"
                  value={channel}
                  disabled={pending}
                  onChange={(event) => chooseChannel(event.target.value as DeliveryChannel)}
                >
                  <option value="email">Email</option>
                  <option value="sms">Consented SMS</option>
                  <option value="imessage">iMessage (optional Mac bridge)</option>
                  <option value="call">Operator-assisted call</option>
                  <option value="provision">Provision a lab line</option>
                  <option value="trunk">Create a lab SIP trunk resource</option>
                </select>
              </div>
              {!management && (
                <>
                  <div className="field">
                    <label htmlFor="infra-contact">Contact</label>
                    <select
                      id="infra-contact"
                      value={contactId}
                      disabled={pending}
                      onChange={(event) => setContactId(event.target.value)}
                    >
                      {data.contacts.map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.id} · {channel === 'email' ? item.email : item.phone}
                          {item.suppressed ? ' · suppressed' : ''}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="field">
                    <label htmlFor="infra-asset">Sending asset</label>
                    <select
                      id="infra-asset"
                      value={assetId}
                      disabled={pending}
                      onChange={(event) => setAssetId(event.target.value)}
                    >
                      {assets.map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.name} · {item.status}
                        </option>
                      ))}
                    </select>
                  </div>
                </>
              )}
              {(channel === 'email' || management) && (
                <div className="field">
                  <label htmlFor="infra-subject">{management ? 'Resource name' : 'Subject'}</label>
                  <input
                    type="text"
                    id="infra-subject"
                    value={subject}
                    required
                    maxLength={200}
                    disabled={pending}
                    onChange={(event) => setSubject(event.target.value)}
                  />
                </div>
              )}
              {channel === 'provision' && (
                <div className="field">
                  <label htmlFor="infra-destination">Synthetic number</label>
                  <input
                    type="text"
                    id="infra-destination"
                    value={destination}
                    pattern="\+120255501[0-9]{2}"
                    required
                    disabled={pending}
                    onChange={(event) => setDestination(event.target.value)}
                  />
                  <span className="meta">
                    Lab range: +12025550100 to +12025550199. No number is purchased.
                  </span>
                </div>
              )}
              {!management && channel !== 'call' && (
                <div className="field">
                  <label htmlFor="infra-message">Message text</label>
                  <textarea
                    id="infra-message"
                    value={body}
                    required
                    maxLength={480}
                    rows={4}
                    disabled={pending}
                    onChange={(event) => setBody(event.target.value)}
                  />
                </div>
              )}
              <div className="field">
                <label htmlFor="infra-scenario">Provider scenario</label>
                <select
                  id="infra-scenario"
                  value={scenario}
                  disabled={pending}
                  onChange={(event) => setScenario(event.target.value as Scenario)}
                >
                  <option value="success">Confirmed outcome</option>
                  {channel !== 'imessage' && (
                    <option value="throttle">Throttle once, then accept</option>
                  )}
                  {!management && channel !== 'imessage' && (
                    <option value="ambiguous">Ambiguous submission</option>
                  )}
                  {channel === 'sms' && <option value="filtered">Carrier filtering event</option>}
                  {channel === 'email' && <option value="bounce">Bounce and suppression</option>}
                </select>
                <p className="meta">
                  {channel === 'imessage'
                    ? 'The simulated Mac bridge accepts the text. An authenticated read verifies delivery.'
                    : scenarioText[scenario]}
                </p>
              </div>
              {channel === 'sms' && (
                <p className="data-note">
                  Explicit SMS consent and a recorded inbound reply are required. An email open
                  never grants permission.
                </p>
              )}
              {channel === 'call' && (
                <p className="data-note">
                  One call per workspace. The adapter models an operator-first bridge. This lab has
                  no SIP media or telephone audio.
                </p>
              )}
              {channel === 'imessage' && (
                <IMessageOption
                  enabled={data.imessageEnabled}
                  contactId={contactId}
                  revision={revision}
                  operator={operator}
                  pending={pending || unconfirmed}
                  ready={setIMessageReady}
                  act={act}
                />
              )}
              <button
                className="button primary"
                disabled={mutationDisabled || (channel === 'imessage' && !imessageReady)}
                type="submit"
              >
                <Send size={15} />
                {pending
                  ? 'Submitting request...'
                  : management
                    ? 'Queue lab resource'
                    : 'Queue lab outreach'}
              </button>
            </form>
          </Panel>
        </div>
      )}
      {tab === 'assets' && (
        <div className="infra-domain-grid">
          {data.assets
            .filter((item) => item.kind === 'email')
            .map((item) => {
              const dns = data.dns.find((entry) => entry.assetId === item.id);
              const ramp = data.ramps.find((entry) => entry.assetId === item.id);
              return (
                <Panel key={item.id} title={item.name} subtitle={item.address}>
                  <div className="panel-body stack">
                    <div className="infra-dns-records">
                      {(['spf', 'dkim', 'dmarc'] as const).map((key) => (
                        <div key={key}>
                          <strong>{key.toUpperCase()}</strong>
                          <Status value={dns?.records[key].state ?? 'not_checked'} />
                        </div>
                      ))}
                    </div>
                    {dns ? (
                      <>
                        <p className="meta">
                          Local DNS fixture · checked <Stamp value={dns.checkedAt} />
                        </p>
                        <p className="data-note">{dns.records.limitation}</p>
                        <details>
                          <summary>Inspect published record evidence</summary>
                          {(['spf', 'dkim', 'dmarc'] as const).map((key) => (
                            <div className="section-gap" key={key}>
                              <strong>{dns.records[key].name}</strong>
                              <pre className="infra-record">
                                {dns.records[key].error ??
                                  (dns.records[key].values.join('\n') || 'No matching record')}
                              </pre>
                            </div>
                          ))}
                        </details>
                      </>
                    ) : (
                      <p className="meta">
                        Inspect the lab DNS records before enabling a volume ramp.
                      </p>
                    )}
                    <div className="row">
                      <button
                        className="button"
                        disabled={mutationDisabled}
                        onClick={() =>
                          void act(
                            `/infrastructure/dns/${item.id}`,
                            { selector: 'mail' },
                            'DNS fixture inspected. Record presence does not prove inbox placement.'
                          )
                        }
                      >
                        Inspect DNS
                      </button>
                      <button className="link-button" onClick={() => asset(item.id)}>
                        Asset details <ArrowRight size={13} />
                      </button>
                    </div>
                    <div className="infra-ramp">
                      <div>
                        <strong>Email volume ramp</strong>
                        <p className="meta">
                          {ramp?.usedToday ?? 0} queued today · daily cap {ramp?.dailyLimit ?? 2}
                        </p>
                      </div>
                      <button
                        className="button"
                        disabled={mutationDisabled}
                        onClick={() =>
                          void act(
                            `/infrastructure/ramps/${item.id}`,
                            { enabled: !ramp?.enabled },
                            ramp?.enabled
                              ? 'Volume ramp paused.'
                              : 'Volume ramp enabled for explicitly opted-in synthetic contacts.'
                          )
                        }
                      >
                        {ramp?.enabled ? 'Pause ramp' : 'Enable ramp'}
                      </button>
                    </div>
                    <p className="meta">
                      Starts at 2 messages a day, adds 2 per day and caps at 20. One scheduled
                      update per opted-in contact per day. Bounces or complaints pause the ramp. No
                      artificial engagement.
                    </p>
                  </div>
                </Panel>
              );
            })}
          <Panel
            title="Phone lines and SIP resources"
            subtitle="Resource creation uses the Twilio REST adapter against the isolated lab."
          >
            <div className="panel-body stack">
              <p>
                Provision a synthetic line or create a secure trunk resource from the request form.
                A confirmed line appears in Sending assets.
              </p>
              <div className="infra-ramp">
                <div>
                  <strong>Local SIP signaling</strong>
                  <p className="meta">
                    {data.sipProbe
                      ? `${data.sipProbe.status} · ${data.sipProbe.roundTripMs.toFixed(2)} ms over ${data.sipProbe.transport}`
                      : 'No probe recorded'}
                  </p>
                  {data.sipProbe && (
                    <p className="meta">
                      Checked <Stamp value={data.sipProbe.checkedAt} />
                    </p>
                  )}
                </div>
                <button
                  className="button"
                  disabled={mutationDisabled}
                  onClick={() =>
                    void act(
                      '/infrastructure/sip/probe',
                      {},
                      'Matched a local SIP OPTIONS response. This does not test audio or carrier routing.'
                    )
                  }
                >
                  Probe SIP peer
                </button>
              </div>
              <p className="data-note">
                A trunk identifier verifies resource creation in the lab. SIP registration, trunk
                routing, audio and carrier connectivity remain unconfigured.
              </p>
              <button
                className="button"
                onClick={() => {
                  setTab('delivery');
                  chooseChannel('provision');
                }}
              >
                Open resource request <ArrowRight size={14} />
              </button>
            </div>
          </Panel>
        </div>
      )}
      {tab === 'inbox' && (
        <div className="infra-grid">
          <Panel
            title="Verified provider replies"
            subtitle="Latest 30 replies. Message contents are available in the authorized detail view."
          >
            {data.inbox.length ? (
              <div className="panel-body stack">
                {data.inbox.map((item) => (
                  <button
                    className="infra-inbox-row"
                    key={item.id}
                    onClick={() => setSelectedReply(item.id)}
                  >
                    <span className="infra-inbox-icon">
                      <Mail size={19} />
                    </span>
                    <span>
                      <strong>Contact · {item.contactId}</strong>
                      <span className="meta">
                        {item.channel} · <Stamp value={item.receivedAt} />
                      </span>
                    </span>
                    <Status value={item.state} />
                    <ArrowRight size={16} />
                  </button>
                ))}
              </div>
            ) : (
              <Empty title="No provider replies recorded">
                Generate a signed synthetic reply to exercise persistence and suppression.
              </Empty>
            )}
          </Panel>
          <Panel
            title="Generate a signed reply"
            subtitle="This sends a webhook through the same verification path."
          >
            <div className="panel-body stack">
              <p className="meta">
                Use a STOP reply to suppress contact c102. Further queued outreach is checked
                against the resulting state.
              </p>
              <button
                className="button"
                disabled={mutationDisabled}
                onClick={() =>
                  void act(
                    '/infrastructure/scenarios',
                    { channel: 'sms', contactId: 'c102', assetId: 'line-cedar', body: 'STOP' },
                    'Signed STOP reply recorded. Contact c102 is now suppressed.'
                  )
                }
              >
                Simulate SMS STOP
              </button>
              <button
                className="button"
                disabled={mutationDisabled}
                onClick={() =>
                  void act(
                    '/infrastructure/scenarios',
                    {
                      channel: 'email',
                      contactId: 'c102',
                      assetId: 'email-north',
                      body: 'Please send the property details.'
                    },
                    'Signed email event verified and its body retrieved through the receiving API.'
                  )
                }
              >
                Simulate email reply
              </button>
              <p className="data-note">
                The email webhook contains metadata. Message text is retrieved separately and
                displayed as plain text.
              </p>
            </div>
          </Panel>
        </div>
      )}
      <div className="row">
        <Status value={data.deliveryWorker.state} />
        <span className="meta">
          Delivery worker · last successful tick{' '}
          <Stamp value={data.deliveryWorker.lastSuccessfulTick} />
        </span>
      </div>
      <details className="infra-boundaries">
        <summary>Capability boundaries and data sources</summary>
        <ul>
          {data.limits.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
        <p className="meta">
          {data.metrics.source}. The original Conversations page retains its earlier simulated
          calling workflow.
        </p>
      </details>
      <Drawer
        open={selectedJob !== null}
        onClose={() => setSelectedJob(null)}
        title="Delivery evidence"
        subtitle="Provider acceptance, recorded events and the current persisted state."
        eyebrow="LOCAL PROVIDER LAB"
      >
        {job.error && <Notice error>{job.error.message}</Notice>}
        {!job.data ? (
          <Loading />
        ) : (
          <div className="stack">
            <Status value={job.data.job.state} />
            <div className="infra-facts">
              <div>
                <span>Channel</span>
                <strong>{job.data.job.channel}</strong>
              </div>
              <div>
                <span>Submission attempts</span>
                <strong>{job.data.job.attempts}</strong>
              </div>
              <div>
                <span>Destination</span>
                <strong>{job.data.job.destination}</strong>
              </div>
              <div>
                <span>Updated</span>
                <Stamp value={job.data.job.updatedAt} />
              </div>
            </div>
            <p className="mono long">
              {job.data.job.providerId ?? 'No provider identifier was returned.'}
            </p>
            {job.data.job.lastError && <Notice warning>{job.data.job.lastError}</Notice>}
            <h3>Verified event history</h3>
            {job.data.events.length ? (
              <div className="timeline">
                {job.data.events.map((event) => (
                  <div className="event" key={event.id}>
                    <span className="event-dot" />
                    <div>
                      <strong>{event.kind}</strong>
                      <p className="meta">
                        {event.source}
                        {event.providerCode ? ` · ${event.providerCode}` : ''}
                      </p>
                      <p className="meta">
                        Occurred <Stamp value={event.occurredAt} />
                        <br />
                        Received <Stamp value={event.receivedAt} />
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <p className="meta">
                No verified provider event is recorded. A queued job or acceptance response is not
                proof of delivery.
              </p>
            )}
            {['unknown', 'accepted'].includes(job.data.job.state) && (
              <>
                <p className="data-note">
                  No automatic resubmission. Verify the existing provider record before taking
                  another action.
                  {job.data.job.channel === 'imessage' &&
                    !job.data.job.providerId &&
                    ' No bridge message GUID was returned. Inspect the Mac; automatic verification is unavailable.'}
                </p>
                <button
                  className="button primary"
                  disabled={
                    mutationDisabled ||
                    (job.data.job.channel === 'imessage' && !job.data.job.providerId)
                  }
                  onClick={() =>
                    void act(
                      `/infrastructure/jobs/${job.data!.job.id}/verify`,
                      { confirmation: 'VERIFY LAB RECORD' },
                      job.data!.job.channel === 'imessage'
                        ? 'Known bridge resource inspected through authenticated reads. No new message was submitted.'
                        : 'Existing lab resource verified through a signed callback. No new outreach was submitted.'
                    )
                  }
                >
                  {job.data.job.channel === 'imessage'
                    ? 'Verify bridge receipt'
                    : 'Verify existing lab record'}
                </button>
              </>
            )}
            {job.data.job.decisionId && (
              <button
                className="link-button"
                onClick={() => {
                  decision(job.data!.job.decisionId!);
                  setSelectedJob(null);
                }}
              >
                Inspect eligibility decision <ArrowRight size={14} />
              </button>
            )}
            {job.data.job.assetId && (
              <button
                className="link-button"
                onClick={() => {
                  asset(job.data!.job.assetId!);
                  setSelectedJob(null);
                }}
              >
                Investigate sending asset <ArrowRight size={14} />
              </button>
            )}
            {error && <Notice error>{error}</Notice>}
          </div>
        )}
      </Drawer>
      <Drawer
        open={selectedReply !== null}
        onClose={() => setSelectedReply(null)}
        title="Provider reply"
        subtitle="Authorized plain-text message detail. Opening this view does not change consent."
        eyebrow="VERIFIED INBOUND EVENT"
      >
        {reply.error && <Notice error>{reply.error.message}</Notice>}
        {!reply.data ? (
          <Loading />
        ) : (
          <div className="stack">
            <Status value={reply.data.state} />
            <p className="meta">
              Contact · {reply.data.contactId} · {reply.data.channel}
            </p>
            <h3>{reply.data.subject || 'SMS reply'}</h3>
            <p className="infra-reply-body">{reply.data.body}</p>
            <button
              className="button primary"
              disabled={mutationDisabled || reply.data.state === 'resolved'}
              onClick={() =>
                void act(
                  `/infrastructure/inbox/${selectedReply}/resolve`,
                  { expectedVersion: reply.data!.version },
                  'Reply marked resolved with an audit record.'
                )
              }
            >
              {reply.data.state === 'resolved' ? 'Resolved' : 'Mark resolved'}
            </button>
            {error && <Notice error>{error}</Notice>}
          </div>
        )}
      </Drawer>
    </div>
  );
}
