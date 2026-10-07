CREATE TABLE campaigns (
 workspace_id text NOT NULL, id text NOT NULL, name text NOT NULL,
 state text NOT NULL DEFAULT 'draft' CHECK(state IN ('draft','active','paused','cancelled')),
 version bigint NOT NULL DEFAULT 1, steps jsonb NOT NULL, asset_ids text[] NOT NULL,
 timezone text NOT NULL, start_hour integer NOT NULL CHECK(start_hour BETWEEN 0 AND 23),
 end_hour integer NOT NULL CHECK(end_hour BETWEEN 1 AND 24 AND end_hour>start_hour),
 weekdays_only boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(workspace_id,id),
 FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE TABLE campaign_enrollments (
 workspace_id text NOT NULL, id text NOT NULL, campaign_id text NOT NULL, contact_id text NOT NULL,
 timezone text NOT NULL, step integer NOT NULL DEFAULT 0 CHECK(step>=0),
 state text NOT NULL DEFAULT 'scheduled' CHECK(state IN ('scheduled','deferred','awaiting_confirmation','held','replied','stopped','completed')),
 reason text NOT NULL DEFAULT 'ENROLLED', next_run_at timestamptz NOT NULL DEFAULT now(),
 enrolled_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), UNIQUE(workspace_id,campaign_id,contact_id),
 FOREIGN KEY(workspace_id,campaign_id) REFERENCES campaigns(workspace_id,id),
 FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id)
);
CREATE TABLE campaign_executions (
 workspace_id text NOT NULL, enrollment_id text NOT NULL, step integer NOT NULL,
 job_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,enrollment_id,step), UNIQUE(workspace_id,job_id),
 FOREIGN KEY(workspace_id,enrollment_id) REFERENCES campaign_enrollments(workspace_id,id),
 FOREIGN KEY(workspace_id,job_id) REFERENCES delivery_jobs(workspace_id,id)
);
CREATE TABLE sending_limits (
 workspace_id text NOT NULL, asset_id text NOT NULL, daily_cap integer NOT NULL DEFAULT 10 CHECK(daily_cap BETWEEN 1 AND 100),
 pacing_seconds integer NOT NULL DEFAULT 30 CHECK(pacing_seconds BETWEEN 1 AND 3600), last_submitted_at timestamptz,
 PRIMARY KEY(workspace_id,asset_id), FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE TABLE email_assessments (
 workspace_id text NOT NULL, id text NOT NULL, asset_id text NOT NULL, assessment jsonb NOT NULL,
 source text NOT NULL, checked_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE TABLE email_fixture_state (
 workspace_id text NOT NULL, asset_id text NOT NULL, scenario text NOT NULL CHECK(scenario IN ('healthy','broken','timeout','conflicting')),
 PRIMARY KEY(workspace_id,asset_id), FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE INDEX campaign_due ON campaign_enrollments(workspace_id,next_run_at) WHERE state IN ('scheduled','deferred','awaiting_confirmation','held');
CREATE INDEX email_assessment_history ON email_assessments(workspace_id,asset_id,checked_at DESC);
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['campaigns','campaign_enrollments','campaign_executions','sending_limits','email_assessments','email_fixture_state'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY tenant_scope ON %I USING(workspace_id=current_setting(''app.workspace_id'',true)) WITH CHECK(workspace_id=current_setting(''app.workspace_id'',true))',t);
  EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON %I TO ops_app',t);
 END LOOP;
END $$;
REVOKE UPDATE ON campaign_executions,email_assessments FROM ops_app;
