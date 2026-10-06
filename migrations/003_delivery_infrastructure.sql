CREATE TABLE delivery_jobs (
 workspace_id text NOT NULL, id text NOT NULL, actor_id text NOT NULL, actor_name text NOT NULL,
 request_key text NOT NULL, request_hash text NOT NULL,
 channel text NOT NULL CHECK(channel IN ('sms','call','email','provision','trunk')),
 contact_id text, asset_id text, destination text NOT NULL, subject text NOT NULL DEFAULT '', body text NOT NULL DEFAULT '',
 purpose text NOT NULL DEFAULT 'outreach' CHECK(purpose IN ('outreach','ramp','management')),
 scenario text NOT NULL DEFAULT 'success' CHECK(scenario IN ('success','throttle','ambiguous','filtered','bounce')),
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','submitting','accepted','delivered','completed','failed','unknown','suppressed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0), provider_id text, last_error text,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), lease_until timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), UNIQUE(workspace_id,actor_id,request_key), UNIQUE(workspace_id,provider_id),
 FOREIGN KEY(workspace_id) REFERENCES workspaces(id),
 FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE INDEX delivery_ready ON delivery_jobs(workspace_id,next_attempt_at) WHERE state='queued';
CREATE UNIQUE INDEX delivery_single_call ON delivery_jobs(workspace_id) WHERE channel='call' AND state IN ('queued','submitting','accepted','unknown');
CREATE TABLE delivery_events (
 workspace_id text NOT NULL, id text NOT NULL, job_id text NOT NULL, kind text NOT NULL,
 source text NOT NULL, provider_code text, occurred_at timestamptz NOT NULL, received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id,job_id) REFERENCES delivery_jobs(workspace_id,id)
);
CREATE INDEX delivery_events_job ON delivery_events(workspace_id,job_id,received_at);
CREATE TABLE channel_permissions (
 workspace_id text NOT NULL, contact_id text NOT NULL, channel text NOT NULL CHECK(channel='email'),
 granted_at timestamptz NOT NULL, evidence text NOT NULL,
 PRIMARY KEY(workspace_id,contact_id,channel), FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id)
);
CREATE TABLE infrastructure_inbox (
 workspace_id text NOT NULL, id text NOT NULL, contact_id text NOT NULL, asset_id text NOT NULL,
 channel text NOT NULL CHECK(channel IN ('sms','email')), body text NOT NULL, subject text NOT NULL DEFAULT '',
 state text NOT NULL DEFAULT 'open' CHECK(state IN ('open','resolved')), version bigint NOT NULL DEFAULT 1,
 received_at timestamptz NOT NULL DEFAULT now(), source text NOT NULL,
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE TABLE infrastructure_dns (
 workspace_id text NOT NULL, asset_id text NOT NULL, records jsonb NOT NULL, checked_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,asset_id), FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE TABLE email_ramps (
 workspace_id text NOT NULL, asset_id text NOT NULL, started_at timestamptz NOT NULL DEFAULT now(),
 enabled boolean NOT NULL DEFAULT false, daily_start integer NOT NULL DEFAULT 2 CHECK(daily_start BETWEEN 1 AND 20),
 daily_step integer NOT NULL DEFAULT 2 CHECK(daily_step BETWEEN 0 AND 20), maximum integer NOT NULL DEFAULT 20 CHECK(maximum BETWEEN 1 AND 100),
 PRIMARY KEY(workspace_id,asset_id), FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['delivery_jobs','delivery_events','channel_permissions','infrastructure_inbox','infrastructure_dns','email_ramps'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY tenant_scope ON %I USING (workspace_id = current_setting(''app.workspace_id'',true)) WITH CHECK (workspace_id = current_setting(''app.workspace_id'',true))',t);
  EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON %I TO ops_app',t);
 END LOOP;
END $$;
REVOKE UPDATE ON delivery_events,channel_permissions FROM ops_app;
