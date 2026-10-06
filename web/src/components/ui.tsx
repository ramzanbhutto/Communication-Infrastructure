import { useId, useRef, useState, type ReactNode } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { AlertCircle, ArrowUpRight, CheckCircle2, LoaderCircle, Search, X } from 'lucide-react';
import type { Audit, Sample } from '../api/types';

export function SearchField({
  label,
  placeholder,
  value,
  onChange
}: {
  label: string;
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const clear = () => {
    onChange('');
    input.current?.focus();
  };
  return (
    <div className="search">
      <Search size={17} className="search-icon" aria-hidden="true" />
      <input
        ref={input}
        type="search"
        aria-label={label}
        placeholder={placeholder}
        value={value}
        maxLength={100}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && value) {
            event.preventDefault();
            clear();
          }
        }}
      />
      {value && (
        <button
          type="button"
          className="search-clear"
          aria-label="Clear search"
          title="Clear search (Esc)"
          onClick={clear}
        >
          <X size={15} aria-hidden="true" />
        </button>
      )}
    </div>
  );
}

export function Badge({ children, tone = '' }: { children: ReactNode; tone?: string }) {
  return <span className={`badge ${tone}`}>{children}</span>;
}
export function statusTone(status: string): string {
  if (
    [
      'active',
      'available',
      'ready',
      'allowed',
      'delivered',
      'completed',
      'present',
      'responsive'
    ].includes(status)
  )
    return 'good';
  if (['blocked', 'unavailable', 'opt-out', 'failed', 'suppressed'].includes(status))
    return 'blocked';
  return 'warning';
}
export function Status({ value }: { value: string }) {
  return (
    <Badge tone={statusTone(value)}>
      <span className="dot" />
      {value[0]?.toUpperCase()}
      {value.slice(1).replaceAll('_', ' ')}
    </Badge>
  );
}
export function Notice({
  children,
  error = false,
  warning = false,
  onClose
}: {
  children: ReactNode;
  error?: boolean;
  warning?: boolean;
  onClose?: () => void;
}) {
  return (
    <div
      className={`notice ${error ? 'error' : warning ? 'warning' : ''}`}
      role={error ? 'alert' : 'status'}
    >
      {error || warning ? <AlertCircle size={16} /> : <CheckCircle2 size={16} />}
      <div>{children}</div>
      {onClose && (
        <button aria-label="Dismiss notice" onClick={onClose}>
          <X size={15} />
        </button>
      )}
    </div>
  );
}
export function Loading({ text = 'Loading recorded data…' }: { text?: string }) {
  return (
    <div className="loading" role="status">
      <LoaderCircle className="spinner" size={22} />
      <span>{text}</span>
    </div>
  );
}
export function Empty({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="empty">
      <CheckCircle2 size={24} />
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}
export function Panel({
  title,
  subtitle,
  action,
  children
}: {
  title?: string;
  subtitle?: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      {title && (
        <div className="panel-header">
          <div>
            <h2>{title}</h2>
            {subtitle && <p>{subtitle}</p>}
          </div>
          {action}
        </div>
      )}
      {children}
    </section>
  );
}
export function TextLink({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return (
    <button className="link-button" onClick={onClick}>
      {children}
      <ArrowUpRight size={13} />
    </button>
  );
}
export function Stamp({ value }: { value: string | Date | null }) {
  return (
    <time dateTime={value ? new Date(value).toISOString() : undefined}>
      {value
        ? new Date(value).toLocaleString('en-GB', {
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit'
          })
        : 'Not recorded'}
    </time>
  );
}
export function Drawer({
  open,
  onClose,
  title,
  subtitle,
  eyebrow,
  children
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  subtitle: string;
  eyebrow: string;
  children: ReactNode;
}) {
  const opener = useRef<HTMLElement | null>(null);
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(value) => {
        if (!value) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="overlay" />
        <Dialog.Content
          className="drawer"
          onOpenAutoFocus={() => {
            opener.current =
              document.activeElement instanceof HTMLElement ? document.activeElement : null;
          }}
          onCloseAutoFocus={(event) => {
            if (opener.current?.isConnected) {
              event.preventDefault();
              opener.current.focus();
            }
          }}
        >
          <header className="drawer-head">
            <div>
              <span className="eyebrow">{eyebrow}</span>
              <Dialog.Title>{title}</Dialog.Title>
              <Dialog.Description className="meta">{subtitle}</Dialog.Description>
            </div>
            <Dialog.Close className="button icon-button" aria-label="Close investigation">
              <X size={17} />
            </Dialog.Close>
          </header>
          <div className="drawer-body">{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function AuditTrail({
  events,
  onAsset,
  onDecision
}: {
  events: Audit[];
  onAsset?: (id: string) => void;
  onDecision?: (id: string) => void;
}) {
  if (!events.length) return <p className="meta">No audit events are recorded in this view.</p>;
  return (
    <div className="timeline">
      {events.map((event) => (
        <div className="event" key={event.id}>
          <span className="event-dot" />
          <div>
            <p>
              <strong>{event.action.replaceAll('.', ' · ').replaceAll('_', ' ')}</strong>
            </p>
            {event.reason && <p className="muted long">{event.reason}</p>}
            <div className="meta">
              {event.actorName} · <Stamp value={event.recordedAt} />
            </div>
            {event.assetId && onAsset && (
              <TextLink onClick={() => onAsset(event.assetId!)}>Investigate asset</TextLink>
            )}
            {event.recordId?.startsWith('dec-') && onDecision && (
              <TextLink onClick={() => onDecision(event.recordId!)}>Recorded decision</TextLink>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}
export function SampleChart({ samples }: { samples: Sample[] }) {
  const selectorId = useId();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const valid = Array.isArray(samples) ? samples.filter((sample) => sample.attempts > 0) : [];
  if (!valid.length) return <p className="meta">No measured provider samples are available.</p>;
  const matchedIndex = valid.findIndex((sample) => sample.id === selectedId);
  const actualIndex = matchedIndex < 0 ? valid.length - 1 : matchedIndex;
  const selected = valid[actualIndex];
  const rates = valid.map((sample) => (sample.filtered / sample.attempts) * 100);
  const max = Math.max(12, ...rates) * 1.15;
  const points = rates.map((rate, index) => [
    12 + (index / Math.max(1, rates.length - 1)) * 256,
    115 - (rate / max) * 98
  ]);
  const path = points
    .map(([x, y], index) => `${index ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`)
    .join(' ');
  const threshold = 115 - (5 / max) * 98;
  const last = points.at(-1)!;
  return (
    <div>
      <svg
        className="chart"
        viewBox="0 0 280 130"
        role="img"
        aria-label={`Measured filter rates: ${rates.map((rate) => `${rate.toFixed(1)}%`).join(', ')}. Restoration threshold 5%.`}
      >
        <path d="M12 115H268M12 66H268M12 17H268" stroke="#e1e7ee" />
        <path d={`${path} L${last[0]},115 L12,115 Z`} fill="#fff3dc" />
        <path d={`M12 ${threshold}H268`} stroke="#bbcccf" strokeDasharray="4 4" />
        <text x="14" y={threshold - 5} fontSize="8" fill="#788793">
          5% threshold
        </text>
        <path d={path} fill="none" stroke="#a77828" strokeWidth="2.3" />
        {points.map(([x, y], index) => (
          <circle
            key={index}
            cx={x}
            cy={y}
            r={index === actualIndex ? 5 : 2.5}
            fill={index === actualIndex ? '#315de0' : '#a77828'}
            stroke={index === actualIndex ? '#ffffff' : 'none'}
            strokeWidth="2"
          />
        ))}
      </svg>
      <div className="chart-labels">
        <Stamp value={valid[0].observedAt} />
        <Stamp value={valid.at(-1)!.observedAt} />
      </div>
      <div className="sample-inspector">
        <label htmlFor={selectorId}>
          Select observation
          <span>
            {actualIndex + 1} / {valid.length}
          </span>
        </label>
        <input
          id={selectorId}
          type="range"
          min={0}
          max={valid.length - 1}
          step={1}
          value={actualIndex}
          disabled={valid.length === 1}
          aria-valuetext={`${rates[actualIndex].toFixed(1)}% filtered, ${selected.filtered} of ${selected.attempts} attempts, ${selected.spamLabel ? 'spam label present' : 'no spam label'}, ${new Date(selected.observedAt).toLocaleString('en-GB')}`}
          onChange={(event) => setSelectedId(valid[Number(event.target.value)].id)}
        />
        <div className="sample-reading" aria-live="polite">
          <strong>{rates[actualIndex].toFixed(1)}%</strong>
          <span>
            {selected.filtered} / {selected.attempts} filtered
            <small>{selected.spamLabel ? 'Spam label present' : 'No spam label'}</small>
          </span>
        </div>
      </div>
    </div>
  );
}
