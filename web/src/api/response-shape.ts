// Catch incomplete API versions at the boundary rather than crashing a screen.
// Detailed field types live in types.ts and are exercised by integration tests.
type FieldKind = 'array' | 'object' | 'number' | 'string' | 'boolean';
const shapes: [RegExp, Record<string, FieldKind>][] = [
  [
    /^\/infrastructure$/,
    {
      mode: 'string',
      liveEnabled: 'boolean',
      imessageEnabled: 'boolean',
      jobs: 'array',
      assets: 'array',
      contacts: 'array',
      inbox: 'array',
      dns: 'array',
      ramps: 'array',
      metrics: 'object',
      limits: 'array',
      observedAt: 'string'
    }
  ],
  [/^\/infrastructure\/jobs\//, { job: 'object', events: 'array', observedAt: 'string' }],
  [
    /^\/infrastructure\/inbox\//,
    {
      body: 'string',
      subject: 'string',
      contactId: 'string',
      channel: 'string',
      state: 'string',
      version: 'number'
    }
  ],
  [/^\/session$/, { user: 'object', demo: 'boolean' }],
  [
    /^\/desk(?:\?|$)/,
    {
      assets: 'array',
      featuredHistory: 'array',
      issues: 'array',
      decisions: 'array',
      activity: 'array',
      metrics: 'object',
      services: 'array',
      queue: 'object',
      observedAt: 'string'
    }
  ],
  [/^\/assets(?:\?|$)/, { items: 'array', total: 'number', observedAt: 'string' }],
  [
    /^\/assets\//,
    {
      asset: 'object',
      samples: 'array',
      audit: 'array',
      restoreProblems: 'array',
      observedAt: 'string'
    }
  ],
  [/^\/contacts(?:\?|$)/, { items: 'array' }],
  [/^\/contacts\//, { contact: 'object', eligibility: 'object', source: 'string' }],
  [
    /^\/decisions(?:\?|$)/,
    { items: 'array', total: 'number', page: 'number', pageSize: 'number', observedAt: 'string' }
  ],
  [/^\/decisions\//, { decision: 'object', explanation: 'string', explanationSource: 'string' }],
  [
    /^\/conversations(?:\?|$)/,
    { replies: 'array', calls: 'array', assets: 'array', queue: 'object', observedAt: 'string' }
  ]
];
export function validReadShape(path: string, value: Record<string, unknown>): boolean {
  const shape = shapes.find(([pattern]) => pattern.test(path))?.[1];
  if (!shape) return true;
  return Object.entries(shape).every(([field, kind]) => {
    const item = value[field];
    if (kind === 'array') return Array.isArray(item);
    if (kind === 'object') return item !== null && typeof item === 'object' && !Array.isArray(item);
    if (kind === 'number') return typeof item === 'number' && Number.isFinite(item);
    return typeof item === kind;
  });
}
