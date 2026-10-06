CREATE TABLE workspaces (id text PRIMARY KEY, name text NOT NULL);
CREATE TABLE users (
  id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id),
  name text NOT NULL, role text NOT NULL CHECK (role IN ('operator','viewer'))
);
CREATE TABLE sessions (
  token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id),
  expires_at timestamptz NOT NULL DEFAULT now() + interval '8 hours'
);
CREATE FUNCTION resolve_session(p_hash text)
RETURNS TABLE(id text, workspace_id text, name text, role text)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS $$
 SELECT u.id,u.workspace_id,u.name,u.role FROM public.sessions s
 JOIN public.users u ON u.id=s.user_id WHERE s.token_hash=p_hash AND s.expires_at>now();
$$;
CREATE FUNCTION demo_login(p_id text, p_hash text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
 IF p_id NOT IN ('demo-operator','demo-viewer') THEN RETURN false; END IF;
 DELETE FROM public.sessions WHERE expires_at < now();
 INSERT INTO public.sessions(token_hash,user_id) VALUES(p_hash,p_id);
 RETURN true;
END;
$$;
CREATE FUNCTION revoke_session(p_hash text) RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS $$
 DELETE FROM public.sessions WHERE token_hash=p_hash;
$$;
REVOKE ALL ON FUNCTION resolve_session(text),demo_login(text,text),revoke_session(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION resolve_session(text),demo_login(text,text),revoke_session(text) TO ops_app;

CREATE TABLE demo_state (
 workspace_id text PRIMARY KEY REFERENCES workspaces(id), generation bigint NOT NULL DEFAULT 1,
 replies_paused boolean NOT NULL DEFAULT true, reset_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE assets (
 workspace_id text NOT NULL REFERENCES workspaces(id), id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('phone','email')), name text NOT NULL, address text NOT NULL,
 status text NOT NULL CHECK(status IN ('active','quarantined')), version bigint NOT NULL DEFAULT 1,
 quarantined_at timestamptz, quarantine_reason text, email_config jsonb,
 PRIMARY KEY(workspace_id,id)
);
CREATE TABLE observations (
 workspace_id text NOT NULL, id bigserial, asset_id text NOT NULL,
 attempts integer NOT NULL CHECK(attempts>=0), filtered integer NOT NULL CHECK(filtered>=0 AND filtered<=attempts),
 spam_label boolean NOT NULL DEFAULT false, observed_at timestamptz NOT NULL DEFAULT now(),
 source text NOT NULL DEFAULT 'simulated_provider', PRIMARY KEY(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id) ON DELETE CASCADE
);
CREATE INDEX observations_asset_time ON observations(workspace_id,asset_id,observed_at DESC,id DESC);
CREATE TABLE contacts (
 workspace_id text NOT NULL, id text NOT NULL, name text NOT NULL,
 phone text NOT NULL, email text NOT NULL, dnc boolean NOT NULL DEFAULT false,
 opted_out_at timestamptz, sms_consent_at timestamptz, consent_source text,
 warm_signal text, warm_at timestamptz, email_opened_at timestamptz,
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id) REFERENCES workspaces(id),
 CHECK((sms_consent_at IS NULL)=(consent_source IS NULL))
);
CREATE TABLE decisions (
 workspace_id text NOT NULL, id text NOT NULL, contact_id text NOT NULL, asset_id text,
 channel text NOT NULL CHECK(channel IN ('sms','call')), outcome text NOT NULL CHECK(outcome IN ('allowed','blocked','deferred')),
 reason text NOT NULL, evidence jsonb NOT NULL, recorded_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE INDEX decisions_filter ON decisions(workspace_id,recorded_at DESC,channel,outcome);
CREATE TABLE audit (
 workspace_id text NOT NULL, id bigserial, actor_id text NOT NULL, actor_name text NOT NULL,
 action text NOT NULL, asset_id text, record_id text, reason text,
 recorded_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(workspace_id,id),
 FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE INDEX audit_context ON audit(workspace_id,asset_id,recorded_at DESC);
CREATE TABLE operations (
 workspace_id text NOT NULL, actor_id text NOT NULL, request_key text NOT NULL,
 request_hash text NOT NULL, http_status integer NOT NULL, result jsonb NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(workspace_id,actor_id,request_key)
);
CREATE TABLE calls (
 workspace_id text NOT NULL, id text NOT NULL, contact_id text NOT NULL, asset_id text NOT NULL,
 decision_id text NOT NULL, status text NOT NULL CHECK(status IN ('active','completed')),
 outcome text, started_at timestamptz NOT NULL DEFAULT now(), finish_at timestamptz NOT NULL,
 completed_at timestamptz, actor_id text NOT NULL, actor_name text NOT NULL,
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id),
 FOREIGN KEY(workspace_id,decision_id) REFERENCES decisions(workspace_id,id)
);
CREATE UNIQUE INDEX one_active_call_per_workspace ON calls(workspace_id) WHERE status='active';
CREATE TABLE messages (
 workspace_id text NOT NULL, id text NOT NULL, contact_id text NOT NULL, asset_id text NOT NULL,
 decision_id text NOT NULL, body text NOT NULL CHECK(length(body) BETWEEN 1 AND 480),
 delivered_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(workspace_id,id),
 FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id),
 FOREIGN KEY(workspace_id,decision_id) REFERENCES decisions(workspace_id,id)
);
CREATE TABLE outbox (
 workspace_id text NOT NULL, id text NOT NULL, generation bigint NOT NULL,
 payload jsonb NOT NULL, published_at timestamptz, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id) REFERENCES workspaces(id)
);
CREATE INDEX outbox_unpublished ON outbox(workspace_id,created_at) WHERE published_at IS NULL;
CREATE TABLE replies (
 workspace_id text NOT NULL, id text NOT NULL, contact_id text NOT NULL, asset_id text NOT NULL,
 body text NOT NULL, tag text NOT NULL CHECK(tag IN ('interested','opt-out','question')),
 received_at timestamptz NOT NULL, persisted_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(workspace_id,id), FOREIGN KEY(workspace_id,contact_id) REFERENCES contacts(workspace_id,id),
 FOREIGN KEY(workspace_id,asset_id) REFERENCES assets(workspace_id,id)
);
CREATE INDEX replies_recent ON replies(workspace_id,received_at DESC);

DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['demo_state','assets','observations','contacts','decisions','audit','operations','calls','messages','outbox','replies'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',t);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',t);
  EXECUTE format('CREATE POLICY tenant_scope ON %I USING (workspace_id = current_setting(''app.workspace_id'',true)) WITH CHECK (workspace_id = current_setting(''app.workspace_id'',true))',t);
  EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON %I TO ops_app',t);
 END LOOP;
END $$;
GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO ops_app;
