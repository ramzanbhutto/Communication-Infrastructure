import { useEffect, useState } from 'react';
import { CheckCheck, MessageCircle, RefreshCw } from 'lucide-react';
import { useRead } from '../api/use-read';
import { Notice, Stamp } from './ui';

type Compatibility = {
  state: string;
  compatible: boolean;
  eligible?: boolean;
  reason?: string;
  contactId?: string;
  source: string;
  checkedAt: string;
  message: string;
};
export function IMessageOption({
  enabled,
  contactId,
  revision,
  operator,
  pending,
  ready,
  act
}: {
  enabled: boolean;
  contactId: string;
  revision: number;
  operator: boolean;
  pending: boolean;
  ready: (value: boolean) => void;
  act: (path: string, data: unknown, success: string) => Promise<boolean>;
}) {
  const [checkedContact, setCheckedContact] = useState<string | null>(null);
  const [checks, setChecks] = useState(0);
  const check = useRead<Compatibility>(
    checkedContact === contactId
      ? `/imessage/compatibility?contactId=${encodeURIComponent(contactId)}`
      : null,
    revision + checks
  );
  useEffect(() => {
    ready(
      Boolean(
        enabled &&
        checkedContact === contactId &&
        check.data?.compatible &&
        check.data.eligible &&
        !check.error &&
        !check.refreshing
      )
    );
  }, [enabled, checkedContact, contactId, check.data, check.error, check.refreshing, ready]);
  const mutationDisabled = !operator || !enabled || pending;
  return (
    <section className="imessage-option" aria-label="Optional iMessage bridge">
      <div className="row">
        <MessageCircle size={18} />
        <strong>iMessage via Mac bridge</strong>
      </div>
      <p className="meta">
        Optional text channel with its own permission check. The local bridge simulates AppleScript
        sending and authenticated message reads. No Mac or Apple account is connected.
      </p>
      {!enabled ? (
        <Notice warning>
          The option is disabled. Start the demo with IMESSAGE_LAB=true and INFRA_LAB=true to
          explore it.
        </Notice>
      ) : (
        <>
          <button
            type="button"
            className="button"
            disabled={pending || check.refreshing}
            onClick={() => {
              setCheckedContact(contactId);
              setChecks((value) => value + 1);
            }}
          >
            <RefreshCw size={14} />
            {check.refreshing ? 'Checking bridge...' : 'Check iMessage compatibility'}
          </button>
          {check.error && (
            <Notice error>{check.error.message} Compatibility could not be established.</Notice>
          )}
          {checkedContact === contactId && check.data && (
            <div className="imessage-assessment" role="status">
              <strong>
                {check.data.compatible
                  ? 'Existing iMessage chat verified'
                  : 'iMessage chat unavailable'}
              </strong>
              <p>{check.data.message}</p>
              {check.data.compatible && (
                <p>
                  {check.data.eligible
                    ? 'Explicit iMessage permission is recorded.'
                    : `Sending is blocked: ${check.data.reason}.`}
                </p>
              )}
              <span className="meta">
                {check.data.source} · Checked <Stamp value={check.data.checkedAt} />
              </span>
            </div>
          )}
          <button
            type="button"
            className="button"
            disabled={mutationDisabled || !check.data?.compatible || checkedContact !== contactId}
            onClick={() =>
              void act(
                '/imessage/sync',
                { contactId },
                'Authenticated bridge messages synchronized. Review the provider inbox and job evidence.'
              )
            }
          >
            Sync bridge messages
          </button>
          <details className="imessage-scenarios">
            <summary>Local iMessage scenarios</summary>
            <p className="meta">
              Scenarios use the opted-in synthetic contact c102. They never send to an Apple device.
              STOP suppresses subsequent outreach across channels.
            </p>
            <div className="action-buttons">
              <button
                type="button"
                className="button"
                disabled={mutationDisabled || contactId !== 'c102'}
                onClick={() =>
                  void act(
                    '/imessage/scenarios',
                    { kind: 'reply', body: 'Could you send the property details here?' },
                    'Simulated iMessage reply imported into the provider inbox.'
                  )
                }
              >
                Simulate iMessage reply
              </button>
              <button
                type="button"
                className="button"
                disabled={mutationDisabled || contactId !== 'c102'}
                onClick={() =>
                  void act(
                    '/imessage/scenarios',
                    { kind: 'read' },
                    'Simulated read timestamps synchronized. Open a delivered iMessage job to inspect the recorded receipt.'
                  )
                }
              >
                <CheckCheck size={14} />
                Simulate read receipt
              </button>
              <button
                type="button"
                className="button"
                disabled={mutationDisabled || contactId !== 'c102'}
                onClick={() =>
                  void act(
                    '/imessage/scenarios',
                    { kind: 'reply', body: 'STOP' },
                    'Simulated iMessage STOP imported. Contact c102 is now suppressed.'
                  )
                }
              >
                Simulate iMessage STOP
              </button>
            </div>
          </details>
          {!operator && (
            <p className="meta">
              Reviewers can check compatibility and inspect evidence. Sending, synchronization and
              simulations require an operator.
            </p>
          )}
        </>
      )}
    </section>
  );
}
